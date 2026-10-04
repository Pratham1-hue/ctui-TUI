// Package ui implements the ctui dashboard on top of Bubble Tea.
package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"ctui/internal/engine"
)

type tab int

const (
	tabContainers tab = iota
	tabImages
	tabVolumes
	numTabs
)

func (t tab) String() string { return [...]string{"Containers", "Images", "Volumes"}[t] }

type screen int

const (
	screenList screen = iota
	screenLogs
	screenInspect
)

type sortMode int

const (
	sortState sortMode = iota
	sortName
	sortCPU
	sortMem
	numSorts
)

func (s sortMode) String() string { return [...]string{"state", "name", "cpu", "memory"}[s] }

type toastKind int

const (
	toastInfo toastKind = iota
	toastOK
	toastErr
)

type toast struct {
	text  string
	kind  toastKind
	until time.Time
}

type confirmDialog struct {
	title, body string
	onYes       func() tea.Cmd
}

// Options configures the dashboard.
type Options struct {
	Refresh time.Duration // how often lists are refreshed
	Stats   bool          // sample live CPU / memory usage
	Tail    int           // log lines to load when opening logs
}

const (
	maxLogLines = 5000
	chromeLines = 8 // border (2) + title, tabs, rule (3) + rule, status, keys (3)
)

type logView struct {
	target     engine.Container
	stream     *engine.LogStream
	gen        int // bumped on every (re)connect so stale streams are ignored
	lines      []string
	follow     bool
	wrap       bool
	timestamps bool
	ended      bool
	err        error
	off        int // lines scrolled up from the bottom
	filter     string
}

// Model is the Bubble Tea model for the dashboard.
type Model struct {
	eng  *engine.Engine
	opts Options

	width, height int
	tab           tab
	screen        screen

	containers []engine.Container
	images     []engine.Image
	volumes    []engine.Volume
	stats      map[string]engine.Stats
	version    string
	lastSync   time.Time

	loaded   [numTabs]bool
	fetching [numTabs]bool
	errs     [numTabs]error

	cursor    [numTabs]int
	offset    [numTabs]int
	filter    [numTabs]string
	filtering bool
	showAll   bool
	sort      sortMode

	pending  map[string]string // item key -> verb in progress
	spin     int
	spinning bool

	toast   *toast
	confirm *confirmDialog
	help    bool
	helpOff int

	logs logView

	inspectTarget engine.Container
	details       *engine.Details
	detailsErr    error
	inspectOff    int
}

// New creates the dashboard model.
func New(eng *engine.Engine, opts Options) *Model {
	if opts.Refresh <= 0 {
		opts.Refresh = 2 * time.Second
	}
	if opts.Tail <= 0 {
		opts.Tail = 200
	}
	return &Model{
		eng:     eng,
		opts:    opts,
		showAll: true,
		stats:   map[string]engine.Stats{},
		pending: map[string]string{},
		logs:    logView{wrap: true},
	}
}

// Init starts the refresh loops.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.tick(), m.fetch(tabContainers), m.versionCmd(), m.startSpin()}
	if m.opts.Stats {
		cmds = append(cmds, m.statsCmd())
	}
	return tea.Batch(cmds...)
}

// Update handles a message and keeps the list scroll position in sync.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := m.update(msg)
	m.offset[m.tab] = scrollWindow(m.cursor[m.tab], m.offset[m.tab], m.listRows(), m.count(m.tab))
	return m, cmd
}

func (m *Model) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampLogs()

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tickMsg:
		cmds := []tea.Cmd{m.tick(), m.fetch(tabContainers)}
		if m.tab != tabContainers {
			cmds = append(cmds, m.fetch(m.tab))
		}
		return tea.Batch(cmds...)

	case spinMsg:
		m.spin++
		if m.busy() {
			return spinCmd()
		}
		m.spinning = false

	case containersMsg:
		m.fetching[tabContainers] = false
		if msg.err != nil {
			m.errs[tabContainers] = msg.err
			return nil
		}
		wasOffline := m.errs[tabContainers] != nil
		m.errs[tabContainers] = nil
		m.loaded[tabContainers] = true
		m.lastSync = time.Now()
		sel := m.selectedKey(tabContainers)
		m.containers = msg.list
		m.reselect(tabContainers, sel)
		if wasOffline {
			m.notify(toastOK, "Connected to "+m.eng.Name)
			if m.version == "" {
				return m.versionCmd()
			}
		}

	case imagesMsg:
		m.fetching[tabImages] = false
		m.errs[tabImages] = msg.err
		if msg.err == nil {
			sel := m.selectedKey(tabImages)
			m.images, m.loaded[tabImages] = msg.list, true
			m.reselect(tabImages, sel)
		}

	case volumesMsg:
		m.fetching[tabVolumes] = false
		m.errs[tabVolumes] = msg.err
		if msg.err == nil {
			sel := m.selectedKey(tabVolumes)
			m.volumes, m.loaded[tabVolumes] = msg.list, true
			m.reselect(tabVolumes, sel)
		}

	case statsTickMsg:
		if m.wantStats() {
			return m.statsCmd()
		}
		return m.statsLater()

	case statsMsg:
		if msg.err == nil {
			m.stats = msg.stats
		}
		return m.statsLater()

	case versionMsg:
		m.version = string(msg)

	case actionMsg:
		delete(m.pending, msg.key)
		if msg.err != nil {
			m.notify(toastErr, fmt.Sprintf("Couldn't %s %s: %v", msg.verb, msg.name, msg.err))
		} else {
			m.notify(toastOK, fmt.Sprintf("%s %s", pastTense(msg.verb), msg.name))
		}
		return m.fetch(msg.tab)

	case clipboardMsg:
		if msg.err != nil {
			m.notify(toastErr, "Couldn't copy: "+msg.err.Error())
		} else {
			m.notify(toastOK, "Copied "+msg.text)
		}

	case shellReadyMsg:
		return tea.ExecProcess(msg.cmd, func(err error) tea.Msg { return shellDoneMsg{msg.name, err} })

	case shellDoneMsg:
		if msg.err != nil {
			m.notify(toastErr, fmt.Sprintf("Shell in %s ended: %v", msg.name, msg.err))
		} else {
			m.notify(toastInfo, "Left shell in "+msg.name)
		}
		return m.fetch(tabContainers)

	case logStartedMsg:
		if msg.gen != m.logs.gen || m.screen != screenLogs {
			msg.stream.Close()
			return nil
		}
		if msg.err != nil {
			m.logs.err, m.logs.ended = msg.err, true
			return nil
		}
		m.logs.stream = msg.stream
		return readLogs(msg.stream)

	case logBatchMsg:
		if msg.stream != m.logs.stream || msg.stream == nil {
			return nil
		}
		m.appendLogs(msg.lines)
		if msg.closed {
			m.logs.ended = true
			m.logs.stream.Close()
			return nil
		}
		return readLogs(msg.stream)

	case detailsMsg:
		if m.screen == screenInspect && msg.id == m.inspectTarget.ID {
			m.details, m.detailsErr = msg.details, msg.err
		}
	}
	return nil
}

// ------------------------------------------------------------------ keys ---

func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()
	if key == "ctrl+c" {
		return m.quit()
	}

	if m.confirm != nil {
		switch key {
		case "y", "Y", "enter":
			onYes := m.confirm.onYes
			m.confirm = nil
			return onYes()
		case "n", "N", "esc", "q":
			m.confirm = nil
		}
		return nil
	}

	if m.help {
		switch key {
		case "?", "esc", "q", "enter":
			m.help = false
		case "up", "k":
			m.scrollHelp(-1)
		case "down", "j":
			m.scrollHelp(1)
		case "pgup":
			m.scrollHelp(-m.bodyRows())
		case "pgdown", " ":
			m.scrollHelp(m.bodyRows())
		}
		return nil
	}

	if m.filtering {
		if m.handleInput(msg) {
			return nil
		}
	}

	switch m.screen {
	case screenLogs:
		return m.logKeys(key)
	case screenInspect:
		return m.inspectKeys(key)
	}
	return m.listKeys(key)
}

// handleInput edits the active filter. It returns false when the key should
// also be handled as a normal key (e.g. arrows).
func (m *Model) handleInput(msg tea.KeyMsg) bool {
	target := &m.filter[m.tab]
	if m.screen == screenLogs {
		target = &m.logs.filter
	}
	switch msg.Type {
	case tea.KeyEsc:
		*target = ""
		m.filtering = false
	case tea.KeyEnter:
		m.filtering = false
	case tea.KeyBackspace:
		if s := *target; s != "" {
			_, size := utf8.DecodeLastRuneInString(s)
			*target = s[:len(s)-size]
		}
	case tea.KeyCtrlU:
		*target = ""
	case tea.KeySpace:
		*target += " "
	case tea.KeyRunes:
		*target += string(msg.Runes)
	case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown:
		m.filtering = false
		return false
	default:
		return true
	}
	if m.screen == screenLogs {
		m.logs.off = 0
		m.logs.follow = true
	} else {
		m.cursor[m.tab] = 0
	}
	return true
}

func (m *Model) listKeys(key string) tea.Cmd {
	switch key {
	case "q":
		return m.quit()
	case "?":
		m.help, m.helpOff = true, 0
	case "esc":
		m.filter[m.tab] = ""
	case "/":
		m.filtering = true
	case "tab", "right", "L":
		return m.switchTab((m.tab + 1) % numTabs)
	case "shift+tab", "left", "H":
		return m.switchTab((m.tab + numTabs - 1) % numTabs)
	case "1", "2", "3":
		return m.switchTab(tab(key[0] - '1'))
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup", "ctrl+b":
		m.move(-m.listRows())
	case "pgdown", "ctrl+f":
		m.move(m.listRows())
	case "home", "g":
		m.cursor[m.tab] = 0
	case "end", "G":
		m.cursor[m.tab] = m.count(m.tab) - 1
		m.move(0)
	case "ctrl+r", "f5":
		m.notify(toastInfo, "Refreshing…")
		return m.fetch(m.tab)
	}

	switch m.tab {
	case tabContainers:
		return m.containerKeys(key)
	case tabImages:
		return m.imageKeys(key)
	case tabVolumes:
		return m.volumeKeys(key)
	}
	return nil
}

func (m *Model) containerKeys(key string) tea.Cmd {
	switch key {
	case "a":
		m.showAll = !m.showAll
		if m.showAll {
			m.notify(toastInfo, "Showing all containers")
		} else {
			m.notify(toastInfo, "Showing running containers only")
		}
		return nil
	case "o":
		m.sort = (m.sort + 1) % numSorts
		if !m.opts.Stats && (m.sort == sortCPU || m.sort == sortMem) {
			m.sort = sortState
		}
		m.notify(toastInfo, "Sorted by "+m.sort.String())
		return nil
	}

	c, ok := m.selectedContainer()
	if !ok {
		return nil
	}
	eng, id, name := m.eng, c.ID, c.Name
	switch key {
	case "enter", "l":
		return m.openLogs(c)
	case "d":
		return m.openInspect(c)
	case "s":
		switch {
		case c.Paused():
			return m.act(id, name, "resume", tabContainers, eng.Unpause)
		case c.Running():
			return m.act(id, name, "stop", tabContainers, eng.Stop)
		default:
			return m.act(id, name, "start", tabContainers, eng.Start)
		}
	case "r":
		return m.act(id, name, "restart", tabContainers, eng.Restart)
	case "p":
		switch {
		case c.Paused():
			return m.act(id, name, "resume", tabContainers, eng.Unpause)
		case c.Running():
			return m.act(id, name, "pause", tabContainers, eng.Pause)
		default:
			m.notify(toastInfo, name+" isn't running")
		}
	case "x", "delete":
		body := "The container will be removed. Its volumes are kept."
		if c.Running() || c.Paused() {
			body = "It is running and will be force-stopped first. Its volumes are kept."
		}
		m.ask("Remove container "+name+"?", body, func() tea.Cmd {
			return m.act(id, name, "remove", tabContainers, eng.Remove)
		})
	case "i", "e":
		if !c.Running() {
			m.notify(toastInfo, name+" isn't running. Press s to start it.")
			return nil
		}
		return m.shell(c)
	case "y":
		return copyToClipboard(c.ID)
	}
	return nil
}

func (m *Model) imageKeys(key string) tea.Cmd {
	vis := m.visibleImages()
	i := m.cursor[tabImages]
	if i < 0 || i >= len(vis) {
		return nil
	}
	img := vis[i]
	switch key {
	case "x", "delete":
		ref := img.Ref()
		body := "Images used by containers can't be removed."
		if n := m.imageUsage()[img.ID]; n > 0 {
			body = fmt.Sprintf("It is used by %d container(s), so removal will likely fail.", n)
		}
		m.ask("Remove image "+ref+"?", body, func() tea.Cmd {
			return m.act(img.ID+ref, ref, "remove", tabImages, m.eng.RemoveImage, ref)
		})
	case "y":
		return copyToClipboard(img.Ref())
	}
	return nil
}

func (m *Model) volumeKeys(key string) tea.Cmd {
	vis := m.visibleVolumes()
	i := m.cursor[tabVolumes]
	if i < 0 || i >= len(vis) {
		return nil
	}
	v := vis[i]
	switch key {
	case "x", "delete":
		m.ask("Remove volume "+shortName(v.Name)+"?",
			"All data in it is deleted permanently. Volumes in use can't be removed.",
			func() tea.Cmd {
				return m.act(v.Name, shortName(v.Name), "remove", tabVolumes, m.eng.RemoveVolume)
			})
	case "y":
		return copyToClipboard(v.Name)
	}
	return nil
}

func (m *Model) logKeys(key string) tea.Cmd {
	page := max(1, m.bodyRows()-2)
	switch key {
	case "q", "esc":
		if key == "esc" && m.logs.filter != "" {
			m.logs.filter = ""
			return nil
		}
		m.closeLogs()
		m.screen = screenList
	case "?":
		m.help, m.helpOff = true, 0
	case "/":
		m.filtering = true
	case "up", "k":
		m.scrollLogs(1)
	case "down", "j":
		m.scrollLogs(-1)
	case "pgup", "ctrl+b":
		m.scrollLogs(page)
	case "pgdown", "ctrl+f", " ":
		m.scrollLogs(-page)
	case "home", "g":
		m.scrollLogs(len(m.logs.lines))
	case "end", "G":
		m.logs.off, m.logs.follow = 0, true
	case "f":
		m.logs.follow = !m.logs.follow
		if m.logs.follow {
			m.logs.off = 0
		}
	case "w":
		m.logs.wrap = !m.logs.wrap
		m.clampLogs()
	case "t":
		m.logs.timestamps = !m.logs.timestamps
		return m.reconnectLogs()
	case "r":
		return m.reconnectLogs()
	case "c":
		m.logs.lines, m.logs.off = nil, 0
	case "d":
		return m.openInspect(m.logs.target)
	}
	return nil
}

func (m *Model) inspectKeys(key string) tea.Cmd {
	switch key {
	case "q", "esc":
		m.screen = screenList
	case "?":
		m.help, m.helpOff = true, 0
	case "up", "k":
		m.scrollInspect(-1)
	case "down", "j":
		m.scrollInspect(1)
	case "pgup", "ctrl+b":
		m.scrollInspect(-m.bodyRows())
	case "pgdown", "ctrl+f", " ":
		m.scrollInspect(m.bodyRows())
	case "home", "g":
		m.inspectOff = 0
	case "end", "G":
		m.scrollInspect(1 << 20)
	case "ctrl+r", "f5":
		return m.openInspect(m.inspectTarget)
	case "enter", "l":
		return m.openLogs(m.inspectTarget)
	case "i", "e":
		for _, c := range m.containers {
			if c.ID == m.inspectTarget.ID && c.Running() {
				return m.shell(c)
			}
		}
		m.notify(toastInfo, m.inspectTarget.Name+" isn't running")
	}
	return nil
}

func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if m.confirm != nil || m.help {
		return nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		d := 3
		if msg.Button == tea.MouseButtonWheelUp {
			d = -3
		}
		switch m.screen {
		case screenLogs:
			m.scrollLogs(-d)
		case screenInspect:
			m.scrollInspect(d)
		default:
			m.move(d)
		}
	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionPress || m.screen != screenList {
			return nil
		}
		if msg.Y == 2 { // tab bar
			x := 2
			for t := tab(0); t < numTabs; t++ {
				w := len([]rune(m.tabLabel(t))) + 2
				if msg.X >= x && msg.X < x+w {
					return m.switchTab(t)
				}
				x += w + 1
			}
			return nil
		}
		row := msg.Y - 5 // border, title, tabs, rule, column header
		if row >= 0 && row < m.listRows() {
			if i := m.offset[m.tab] + row; i < m.count(m.tab) {
				m.cursor[m.tab] = i
			}
		}
	}
	return nil
}

// --------------------------------------------------------------- helpers ---

func (m *Model) quit() tea.Cmd {
	m.closeLogs()
	return tea.Quit
}

func (m *Model) switchTab(t tab) tea.Cmd {
	if t < 0 || t >= numTabs {
		return nil
	}
	m.tab = t
	m.filtering = false
	return m.fetch(t)
}

func (m *Model) move(d int) {
	n := m.count(m.tab)
	c := m.cursor[m.tab] + d
	if c >= n {
		c = n - 1
	}
	if c < 0 {
		c = 0
	}
	m.cursor[m.tab] = c
}

func (m *Model) ask(title, body string, onYes func() tea.Cmd) {
	m.confirm = &confirmDialog{title: title, body: body, onYes: onYes}
}

func (m *Model) notify(kind toastKind, text string) {
	d := 4 * time.Second
	if kind == toastErr {
		d = 8 * time.Second
	}
	m.toast = &toast{text: text, kind: kind, until: time.Now().Add(d)}
}

func (m *Model) busy() bool {
	if len(m.pending) > 0 || (!m.loaded[m.tab] && m.errs[m.tab] == nil) {
		return true
	}
	switch m.screen {
	case screenLogs:
		return m.logs.stream == nil && !m.logs.ended
	case screenInspect:
		return m.details == nil && m.detailsErr == nil
	}
	return false
}

func (m *Model) bodyRows() int { return max(1, m.height-chromeLines) }

// listRows is the number of table rows that fit below the column header.
func (m *Model) listRows() int { return max(1, m.bodyRows()-1) }

func (m *Model) count(t tab) int {
	switch t {
	case tabImages:
		return len(m.visibleImages())
	case tabVolumes:
		return len(m.visibleVolumes())
	}
	return len(m.visibleContainers())
}

func (m *Model) selectedContainer() (engine.Container, bool) {
	vis := m.visibleContainers()
	i := m.cursor[tabContainers]
	if i < 0 || i >= len(vis) {
		return engine.Container{}, false
	}
	return vis[i], true
}

func (m *Model) selectedKey(t tab) string {
	i := m.cursor[t]
	switch t {
	case tabImages:
		if vis := m.visibleImages(); i < len(vis) {
			return vis[i].ID + vis[i].Ref()
		}
	case tabVolumes:
		if vis := m.visibleVolumes(); i < len(vis) {
			return vis[i].Name
		}
	default:
		if vis := m.visibleContainers(); i < len(vis) {
			return vis[i].ID
		}
	}
	return ""
}

// reselect keeps the cursor on the same item after a refresh.
func (m *Model) reselect(t tab, key string) {
	if key != "" {
		var keys []string
		switch t {
		case tabImages:
			for _, x := range m.visibleImages() {
				keys = append(keys, x.ID+x.Ref())
			}
		case tabVolumes:
			for _, x := range m.visibleVolumes() {
				keys = append(keys, x.Name)
			}
		default:
			for _, x := range m.visibleContainers() {
				keys = append(keys, x.ID)
			}
		}
		for i, k := range keys {
			if k == key {
				m.cursor[t] = i
				return
			}
		}
	}
	if n := m.count(t); m.cursor[t] >= n {
		m.cursor[t] = max(0, n-1)
	}
}

func (m *Model) visibleContainers() []engine.Container {
	q := strings.ToLower(strings.TrimSpace(m.filter[tabContainers]))
	out := make([]engine.Container, 0, len(m.containers))
	for _, c := range m.containers {
		if !m.showAll && !c.Running() && !c.Paused() && c.State != "restarting" {
			continue
		}
		if q != "" && !matches(q, c.Name, c.Image, c.ID, c.State, c.Status, c.Ports) {
			continue
		}
		out = append(out, c)
	}
	switch m.sort {
	case sortName:
		sort.SliceStable(out, func(a, b int) bool { return strings.ToLower(out[a].Name) < strings.ToLower(out[b].Name) })
	case sortCPU:
		sort.SliceStable(out, func(a, b int) bool {
			return m.stats[out[a].ShortID()].CPU > m.stats[out[b].ShortID()].CPU
		})
	case sortMem:
		sort.SliceStable(out, func(a, b int) bool {
			return m.stats[out[a].ShortID()].MemPerc > m.stats[out[b].ShortID()].MemPerc
		})
	}
	return out
}

func (m *Model) visibleImages() []engine.Image {
	q := strings.ToLower(strings.TrimSpace(m.filter[tabImages]))
	if q == "" {
		return m.images
	}
	var out []engine.Image
	for _, i := range m.images {
		if matches(q, i.Ref(), i.ID, i.Size) {
			out = append(out, i)
		}
	}
	return out
}

func (m *Model) visibleVolumes() []engine.Volume {
	q := strings.ToLower(strings.TrimSpace(m.filter[tabVolumes]))
	if q == "" {
		return m.volumes
	}
	var out []engine.Volume
	for _, v := range m.volumes {
		if matches(q, v.Name, v.Driver, v.Mountpoint) {
			out = append(out, v)
		}
	}
	return out
}

// imageUsage counts containers per image ID.
func (m *Model) imageUsage() map[string]int {
	use := map[string]int{}
	for _, img := range m.images {
		for _, c := range m.containers {
			ci := strings.TrimPrefix(c.Image, "sha256:")
			if c.Image == img.Ref() ||
				(img.Tag == "latest" && c.Image == img.Repository) ||
				(len(ci) >= 12 && strings.HasPrefix(ci, img.ID)) {
				use[img.ID]++
			}
		}
	}
	return use
}

func (m *Model) wantStats() bool {
	if !m.opts.Stats || m.errs[tabContainers] != nil || m.tab != tabContainers || m.screen == screenLogs {
		return false
	}
	for _, c := range m.containers {
		if c.Running() {
			return true
		}
	}
	return false
}

func matches(q string, fields ...string) bool {
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

func shortName(s string) string {
	if len(s) == 64 {
		return s[:12] + "…"
	}
	return s
}

func pastTense(verb string) string {
	switch verb {
	case "stop":
		return "Stopped"
	case "remove":
		return "Removed"
	case "pause":
		return "Paused"
	case "resume":
		return "Resumed"
	}
	return strings.ToUpper(verb[:1]) + verb[1:] + "ed"
}

func progressive(verb string) string {
	switch verb {
	case "stop":
		return "stopping"
	case "remove":
		return "removing"
	case "pause":
		return "pausing"
	case "resume":
		return "resuming"
	}
	return verb + "ing"
}
