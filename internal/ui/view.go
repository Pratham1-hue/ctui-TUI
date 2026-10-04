package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"ctui/internal/engine"
)

// View renders the whole screen.
func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	if m.width < 50 || m.height < 12 {
		msg := bold(cPink).Render("⚡ ctui") + "\n" +
			fg(cMuted).Render(fmt.Sprintf("Window too small (%d×%d).\nNeed at least 50×12.", m.width, m.height))
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, msg)
	}

	w, h := m.width-4, m.bodyRows()
	lines := []string{m.viewTitle(w), m.viewTabs(w), rule(w)}
	lines = append(lines, m.viewBody(w, h)...)
	lines = append(lines, rule(w), m.viewStatus(w), m.viewKeys(w))
	for i := range lines {
		lines[i] = fitANSI(lines[i], w)
	}

	border := cPurple
	if m.errs[tabContainers] != nil {
		border = cRed
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

func (m *Model) spinner() string { return spinnerFrames[m.spin%len(spinnerFrames)] }

// -------------------------------------------------------------- top bars ---

func (m *Model) viewTitle(w int) string {
	left := badge(cInk, cCyan).Render("⚡ CTUI") + " " + bold(cCyan).Render(m.eng.Name)
	if m.version != "" {
		left += fg(cMuted).Render(" v" + m.version)
	}
	if m.loaded[tabContainers] {
		var running, paused, stopped int
		for _, c := range m.containers {
			switch {
			case c.Running():
				running++
			case c.Paused():
				paused++
			default:
				stopped++
			}
		}
		left += fg(cDim).Render("  │  ") + fg(cGreen).Render(fmt.Sprintf("● %d running", running))
		if paused > 0 {
			left += "  " + fg(cYellow).Render(fmt.Sprintf("‖ %d paused", paused))
		}
		left += "  " + fg(cMuted).Render(fmt.Sprintf("○ %d stopped", stopped))
	}

	var right string
	switch {
	case m.errs[tabContainers] != nil:
		right = bold(cRed).Render("● offline")
	case len(m.pending) > 0:
		right = fg(cOrange).Render(fmt.Sprintf("%s %d task(s) running", m.spinner(), len(m.pending)))
	case !m.lastSync.IsZero():
		right = fg(cMuted).Render("↻ " + m.lastSync.Format("15:04:05"))
	}
	return spread(left, right, w)
}

func (m *Model) tabLabel(t tab) string {
	label := fmt.Sprintf("%d %s", t+1, t)
	if m.loaded[t] {
		n := len(m.containers)
		switch t {
		case tabImages:
			n = len(m.images)
		case tabVolumes:
			n = len(m.volumes)
		}
		label += fmt.Sprintf(" %d", n)
	}
	return label
}

func (m *Model) viewTabs(w int) string {
	sep := fg(cDim).Render(" › ")
	switch m.screen {
	case screenLogs:
		left := fg(cMuted).Render("Containers") + sep + bold(cText).Render(m.logs.target.Name) + sep + badge(cInk, cPink).Render("Logs")
		return spread(left, flag("follow", m.logs.follow)+"  "+flag("wrap", m.logs.wrap)+"  "+flag("timestamps", m.logs.timestamps), w)
	case screenInspect:
		left := fg(cMuted).Render("Containers") + sep + bold(cText).Render(m.inspectTarget.Name) + sep + badge(cInk, cPink).Render("Inspect")
		return left
	}

	var parts []string
	for t := tab(0); t < numTabs; t++ {
		if t == m.tab {
			parts = append(parts, badge(cInk, cPink).Render(m.tabLabel(t)))
		} else {
			parts = append(parts, lipgloss.NewStyle().Foreground(cMuted).Padding(0, 1).Render(m.tabLabel(t)))
		}
	}
	left := strings.Join(parts, " ")

	var chips []string
	if f := m.filter[m.tab]; f != "" && !m.filtering {
		chips = append(chips, fg(cYellow).Render("⌕ "+f))
	}
	if m.tab == tabContainers {
		if !m.showAll {
			chips = append(chips, fg(cOrange).Render("running only"))
		}
		if m.sort != sortState {
			chips = append(chips, fg(cMuted).Render("sort: "+m.sort.String()))
		}
	}
	if n := m.count(m.tab); n > 0 {
		chips = append(chips, fg(cMuted).Render(fmt.Sprintf("%d/%d", m.cursor[m.tab]+1, n)))
	}
	return spread(left, strings.Join(chips, fg(cDim).Render(" · ")), w)
}

func flag(name string, on bool) string {
	if on {
		return fg(cGreen).Render("● " + name)
	}
	return fg(cDim).Render("○ " + name)
}

// ------------------------------------------------------------------ body ---

func (m *Model) viewBody(w, h int) []string {
	var lines []string
	switch {
	case m.help:
		lines = m.helpBody(w, h)
	case m.screen == screenLogs:
		lines = m.logsBody(w, h)
	case m.screen == screenInspect:
		lines = m.inspectBody(w, h)
	default:
		lines = m.listBody(w, h)
	}
	lines = padLines(lines, h)
	if m.confirm != nil {
		lines = padLines(overlay(lines, m.confirmBox(w), w), h)
	}
	return lines
}

func padLines(lines []string, h int) []string {
	if len(lines) > h {
		return lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines
}

// panel centers a short message in an h-line area.
func panel(h, w int, msg ...string) []string {
	lines := make([]string, 0, h)
	for i := 0; i < (h-len(msg))/2; i++ {
		lines = append(lines, "")
	}
	for _, l := range msg {
		lines = append(lines, center(l, w))
	}
	return lines
}

func overlay(lines, box []string, w int) []string {
	start := max(0, (len(lines)-len(box))/2)
	for i, b := range box {
		if start+i < len(lines) {
			lines[start+i] = center(b, w)
		}
	}
	return lines
}

func (m *Model) listBody(w, h int) []string {
	t := m.tab
	if err := m.errs[t]; err != nil && (t == tabContainers || !m.loaded[t]) {
		if t == tabContainers {
			return m.offlinePanel(err, w, h)
		}
		return panel(h, w, bold(cRed).Render("✗ Couldn't load "+strings.ToLower(t.String())), "",
			fg(cMuted).Render(runewidth.Truncate(err.Error(), w-4, "…")))
	}
	if !m.loaded[t] {
		return panel(h, w, fg(cCyan).Render(m.spinner()+" Loading "+strings.ToLower(t.String())+"…"))
	}

	var cols []column
	var rows [][]cell
	switch t {
	case tabImages:
		cols, rows = m.imageTable()
	case tabVolumes:
		cols, rows = m.volumeTable()
	default:
		cols, rows = m.containerTable()
	}
	widths := layout(cols, w-2)
	lines := []string{renderHeader(cols, widths, w)}
	if len(rows) == 0 {
		return append(lines, panel(h-1, w, m.emptyMessage()...)...)
	}
	off := scrollWindow(m.cursor[t], m.offset[t], h-1, len(rows))
	for i := off; i < len(rows) && len(lines) < h; i++ {
		lines = append(lines, renderRow(cols, widths, rows[i], i == m.cursor[t], w))
	}
	return lines
}

func (m *Model) offlinePanel(err error, w, h int) []string {
	hint := "Start Docker Desktop. ctui reconnects automatically."
	if m.eng.Name == "podman" {
		hint = "Run `podman machine start`. ctui reconnects automatically."
	}
	msg := []string{bold(cRed).Render("✗ Can't reach the " + m.eng.Name + " engine"), ""}
	for i, l := range wrapPlain(err.Error(), min(w-8, 90)) {
		if i == 3 {
			break
		}
		msg = append(msg, fg(cMuted).Render(l))
	}
	msg = append(msg, "", fg(cText).Render(hint),
		fg(cDim).Render(fmt.Sprintf("%s retrying every %s", m.spinner(), m.opts.Refresh)))
	return panel(h, w, msg...)
}

func (m *Model) emptyMessage() []string {
	if q := m.filter[m.tab]; q != "" {
		return []string{fg(cText).Render(fmt.Sprintf("Nothing matches “%s”", q)), fg(cMuted).Render("Press esc to clear the filter")}
	}
	switch m.tab {
	case tabImages:
		return []string{fg(cText).Render("No images yet"), fg(cMuted).Render("Try: " + m.eng.Name + " pull nginx")}
	case tabVolumes:
		return []string{fg(cText).Render("No volumes")}
	}
	if !m.showAll && len(m.containers) > 0 {
		return []string{fg(cText).Render("No running containers"), fg(cMuted).Render("Press a to show stopped ones too")}
	}
	return []string{fg(cText).Render("No containers yet"), fg(cMuted).Render("Try: " + m.eng.Name + " run -d -p 8080:80 nginx")}
}

// ---------------------------------------------------------------- tables ---

func (m *Model) containerTable() ([]column, [][]cell) {
	cols := []column{
		{title: "STATE", min: 12},
		{title: "NAME", min: 14, max: 36, weight: 3},
		{title: "ID", min: 12, priority: 1},
		{title: "IMAGE", min: 14, max: 40, weight: 3, priority: 5},
		{title: "CPU", min: 6, right: true, priority: 4, off: !m.opts.Stats},
		{title: "MEM", min: 9, right: true, priority: 3, off: !m.opts.Stats},
		{title: "STATUS", min: 14, max: 30, weight: 2, priority: 6},
		{title: "PORTS", min: 12, max: 48, weight: 2, priority: 2},
	}
	vis := m.visibleContainers()
	rows := make([][]cell, len(vis))
	for i, c := range vis {
		icon, label, color := stateLook(c.State)
		state := cell{text: icon + " " + label, color: color}
		if verb, ok := m.pending[c.ID]; ok {
			state = cell{text: m.spinner() + " " + progressive(verb), color: cOrange}
		}
		cpu, mem := cell{text: "-", color: cDim}, cell{text: "-", color: cDim}
		if s, ok := m.stats[c.ShortID()]; ok && c.Running() {
			cpu = cell{text: fmt.Sprintf("%.1f%%", s.CPU), color: loadColor(s.CPU)}
			mem = cell{text: s.Mem, color: loadColor(s.MemPerc)}
		} else if c.Running() {
			cpu.text, mem.text = "…", "…"
		}
		ports := cell{text: compactPorts(c.Ports), color: cYellow}
		if ports.text == "" {
			ports = cell{text: "-", color: cDim}
		}
		rows[i] = []cell{
			state,
			{text: c.Name, color: cText, bold: true},
			{text: c.ShortID(), color: cPink},
			{text: c.Image, color: cMuted},
			cpu,
			mem,
			{text: c.Status, color: statusColor(c.Status)},
			ports,
		}
	}
	return cols, rows
}

func (m *Model) imageTable() ([]column, [][]cell) {
	cols := []column{
		{title: "REPOSITORY", min: 16, max: 56, weight: 4},
		{title: "TAG", min: 8, max: 24, weight: 2},
		{title: "IMAGE ID", min: 12, priority: 2},
		{title: "SIZE", min: 8, right: true, priority: 3},
		{title: "CREATED", min: 13, max: 18, weight: 1, priority: 1},
		{title: "IN USE", min: 7, priority: 4},
	}
	use := m.imageUsage()
	vis := m.visibleImages()
	rows := make([][]cell, len(vis))
	for i, img := range vis {
		repo := cell{text: img.Repository, color: cText, bold: true}
		tag := cell{text: img.Tag, color: cCyan}
		if img.Dangling() {
			repo, tag = cell{text: "<none>", color: cDim}, cell{text: "<none>", color: cDim}
		}
		if verb, ok := m.pending[img.ID+img.Ref()]; ok {
			repo = cell{text: m.spinner() + " " + progressive(verb) + " " + img.Repository, color: cOrange}
		}
		used := cell{text: "-", color: cDim}
		if n := use[img.ID]; n > 0 {
			used = cell{text: fmt.Sprintf("● %d", n), color: cGreen}
		}
		rows[i] = []cell{repo, tag, {text: img.ID, color: cPink}, {text: img.Size, color: cYellow}, {text: img.Created, color: cMuted}, used}
	}
	return cols, rows
}

func (m *Model) volumeTable() ([]column, [][]cell) {
	cols := []column{
		{title: "NAME", min: 20, max: 64, weight: 3},
		{title: "DRIVER", min: 8, priority: 2},
		{title: "MOUNTPOINT", min: 16, weight: 4, priority: 1},
	}
	vis := m.visibleVolumes()
	rows := make([][]cell, len(vis))
	for i, v := range vis {
		name := cell{text: v.Name, color: cText, bold: true}
		if v.Anonymous() {
			name = cell{text: v.Name, color: cMuted}
		}
		if verb, ok := m.pending[v.Name]; ok {
			name = cell{text: m.spinner() + " " + progressive(verb) + " " + v.Name, color: cOrange}
		}
		rows[i] = []cell{name, {text: v.Driver, color: cCyan}, {text: v.Mountpoint, color: cMuted}}
	}
	return cols, rows
}

func loadColor(pct float64) lipgloss.Color {
	switch {
	case pct >= 85:
		return cRed
	case pct >= 50:
		return cYellow
	}
	return cText
}

func statusColor(s string) lipgloss.Color {
	l := strings.ToLower(s)
	switch {
	case strings.Contains(l, "unhealthy"):
		return cRed
	case strings.Contains(l, "(healthy)"):
		return cGreen
	case strings.HasPrefix(l, "exited (0)"):
		return cMuted
	case strings.HasPrefix(l, "exited"):
		return cOrange
	case strings.HasPrefix(l, "up"):
		return cText
	}
	return cMuted
}

// compactPorts shortens docker's port list: drops wildcard hosts and the
// duplicated IPv6 bindings.
func compactPorts(s string) string {
	seen := map[string]bool{}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		for _, pre := range []string{"0.0.0.0:", ":::", "[::]:"} {
			p = strings.TrimPrefix(p, pre)
		}
		p = strings.ReplaceAll(p, "->", "→")
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return strings.Join(out, ", ")
}

// ------------------------------------------------------------------ logs ---

func (m *Model) logsBody(w, h int) []string {
	l := &m.logs
	src := m.logLines()

	var state string
	switch {
	case l.err != nil:
		state = bold(cRed).Render("✗ error")
	case l.ended:
		state = fg(cRed).Render("■ stream ended")
	case l.stream == nil:
		state = fg(cCyan).Render(m.spinner() + " connecting")
	case l.follow:
		state = bold(cGreen).Render("● live")
	default:
		state = bold(cYellow).Render("‖ paused")
	}
	info := fmt.Sprintf("%d lines", len(src))
	if l.filter != "" {
		info = fmt.Sprintf("%d of %d lines match", len(src), len(l.lines))
	}
	if l.off > 0 {
		info += fmt.Sprintf(" · ↑%d", l.off)
	}
	title := bold(cText).Render(l.target.Name) + fg(cMuted).Render("  "+l.target.Image)
	lines := []string{spread(title, state+fg(cDim).Render("  │  ")+fg(cMuted).Render(info), w)}

	rows := h - 1
	switch {
	case l.err != nil:
		return append(lines, panel(rows, w, bold(cRed).Render("Couldn't stream logs"), fg(cMuted).Render(l.err.Error()))...)
	case len(src) == 0 && l.filter != "":
		return append(lines, panel(rows, w, fg(cMuted).Render(fmt.Sprintf("No lines match “%s”", l.filter)))...)
	case len(src) == 0:
		return append(lines, panel(rows, w, fg(cMuted).Render("Waiting for output…"))...)
	}

	end := max(0, len(src)-l.off)
	var out []string
	for i := end - 1; i >= 0 && len(out) < rows; i-- {
		segs := []string{runewidth.Truncate(src[i], w, "…")}
		if l.wrap {
			segs = wrapPlain(src[i], w)
		}
		color := levelColor(src[i])
		for j := len(segs) - 1; j >= 0 && len(out) < rows; j-- {
			out = append(out, m.styleLog(segs[j], color, j == 0))
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return append(lines, out...)
}

func (m *Model) styleLog(seg string, color lipgloss.Color, first bool) string {
	var ts string
	if first && m.logs.timestamps {
		if i := strings.IndexByte(seg, ' '); i > 0 && i <= 40 {
			ts, seg = seg[:i+1], seg[i+1:]
		}
	}
	out := ""
	if ts != "" {
		out = fg(cDim).Render(ts)
	}
	return out + highlight(seg, m.logs.filter, fg(color))
}

func highlight(s, q string, base lipgloss.Style) string {
	ls, lq := strings.ToLower(s), strings.ToLower(q)
	if q == "" || len(ls) != len(s) || len(lq) != len(q) {
		return base.Render(s)
	}
	hl := lipgloss.NewStyle().Foreground(cInk).Background(cYellow)
	var b strings.Builder
	for {
		i := strings.Index(ls, lq)
		if i < 0 {
			break
		}
		if i > 0 {
			b.WriteString(base.Render(s[:i]))
		}
		b.WriteString(hl.Render(s[i : i+len(q)]))
		s, ls = s[i+len(q):], ls[i+len(q):]
	}
	if s != "" {
		b.WriteString(base.Render(s))
	}
	return b.String()
}

func levelColor(line string) lipgloss.Color {
	l := strings.ToLower(line)
	switch {
	case strings.Contains(l, "error"), strings.Contains(l, "fatal"), strings.Contains(l, "panic"),
		strings.Contains(l, "exception"), strings.Contains(l, "traceback"), strings.Contains(l, "critical"):
		return cRed
	case strings.Contains(l, "warn"):
		return cYellow
	case strings.Contains(l, "debug"):
		return cMuted
	}
	return cText
}

// --------------------------------------------------------------- inspect ---

func (m *Model) inspectBody(w, h int) []string {
	title := bold(cText).Render(m.inspectTarget.Name) + fg(cMuted).Render("  "+m.inspectTarget.ShortID())
	rows := h - 1
	switch {
	case m.detailsErr != nil:
		return append([]string{title}, panel(rows, w, bold(cRed).Render("Couldn't inspect container"), fg(cMuted).Render(m.detailsErr.Error()))...)
	case m.details == nil:
		return append([]string{title}, panel(rows, w, fg(cCyan).Render(m.spinner()+" Loading details…"))...)
	}
	all := m.inspectLines()
	off := min(m.inspectOff, max(0, len(all)-rows))
	end := min(len(all), off+rows)
	pos := fg(cMuted).Render(fmt.Sprintf("lines %d–%d of %d", off+1, end, len(all)))
	return append([]string{spread(title, pos, w)}, all[off:end]...)
}

func (m *Model) inspectLines() []string {
	d := m.details
	if d == nil {
		return nil
	}
	var lines []string
	section := func(t string) {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, bold(cPink).Render("▍"+t))
	}
	kvStyled := func(k, v string) { lines = append(lines, "  "+fg(cMuted).Render(fit(k, 13))+v) }
	kv := func(k, v string) {
		if v != "" {
			kvStyled(k, fg(cText).Render(v))
		}
	}
	list := func(items []string, empty string) {
		if len(items) == 0 {
			lines = append(lines, "  "+fg(cDim).Render(empty))
		}
		for _, it := range items {
			if a, b, ok := strings.Cut(it, "\t"); ok {
				kvStyled(a, fg(cText).Render(b))
			} else {
				lines = append(lines, "  "+fg(cText).Render(it))
			}
		}
	}

	section("OVERVIEW")
	kv("Name", d.Name)
	kv("ID", d.ID)
	kv("Image", d.Image)
	icon, label, color := stateLook(d.State)
	state := label
	if d.Health != "" {
		state += " (" + d.Health + ")"
	}
	if d.State == "exited" {
		state += fmt.Sprintf(" · exit code %d", d.ExitCode)
	}
	kvStyled("State", fg(color).Render(icon+" "+state))
	kv("Created", d.Created)
	kv("Started", d.Started)
	if d.State != "running" {
		kv("Finished", d.Finished)
	}
	policy := d.RestartPolicy
	if policy == "" {
		policy = "no"
	}
	kv("Restart", fmt.Sprintf("%s · restarted %d time(s)", policy, d.RestartCount))
	kv("Platform", d.Platform)
	kv("Command", d.Command)
	kv("Workdir", d.WorkingDir)
	kv("User", d.User)
	kv("Hostname", d.Host)

	if s, ok := m.stats[engine.ShortID(d.ID)]; ok {
		section("LIVE USAGE")
		kvStyled("CPU", meter(s.CPU, 20)+fg(cText).Render(fmt.Sprintf(" %.1f%%", s.CPU)))
		kvStyled("Memory", meter(s.MemPerc, 20)+fg(cText).Render(fmt.Sprintf(" %s / %s (%.1f%%)", s.Mem, s.MemLimit, s.MemPerc)))
		kv("Network", s.NetIO+"  (rx / tx)")
		kv("Disk", s.BlockIO+"  (read / write)")
	}

	section("PORTS")
	list(d.Ports, "none")
	section("NETWORKS")
	list(d.Networks, "none")
	section("MOUNTS")
	list(d.Mounts, "none")
	section(fmt.Sprintf("ENVIRONMENT (%d)", len(d.Env)))
	if len(d.Env) == 0 {
		lines = append(lines, "  "+fg(cDim).Render("none"))
	}
	for _, e := range d.Env {
		k, v, _ := strings.Cut(e, "=")
		lines = append(lines, "  "+fg(cCyan).Render(k)+fg(cDim).Render("=")+fg(cText).Render(v))
	}
	section(fmt.Sprintf("LABELS (%d)", len(d.Labels)))
	if len(d.Labels) == 0 {
		lines = append(lines, "  "+fg(cDim).Render("none"))
	}
	for _, l := range d.Labels {
		k, v, _ := strings.Cut(l, "=")
		lines = append(lines, "  "+fg(cMuted).Render(k)+fg(cDim).Render("=")+fg(cText).Render(v))
	}
	return lines
}

// meter draws a w-cell usage bar for a percentage.
func meter(pct float64, w int) string {
	n := int(min(max(pct, 0), 100) / 100 * float64(w))
	if pct > 0 && n == 0 {
		n = 1
	}
	return fg(loadColor(pct)).Render(strings.Repeat("█", n)) + fg(cDim).Render(strings.Repeat("░", w-n))
}

// ------------------------------------------------------------- overlays ---

func (m *Model) confirmBox(w int) []string {
	bw := min(w-4, 62)
	text := lipgloss.NewStyle().Width(bw - 8)
	keys := badge(cInk, cRed).Render("y") + fg(cText).Render(" confirm    ") +
		badge(cInk, cMuted).Render("n") + fg(cText).Render(" cancel")
	content := text.Copy().Foreground(cRed).Bold(true).Render(m.confirm.title) + "\n\n" +
		text.Copy().Foreground(cText).Render(m.confirm.body) + "\n\n" + keys
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cRed).
		Padding(1, 3).
		Width(bw - 2).
		Render(content)
	return strings.Split(box, "\n")
}

type hint struct{ key, desc string }

var helpSections = []struct {
	title string
	keys  []hint
}{
	{"Navigation", []hint{
		{"↑ ↓  k j", "move"}, {"PgUp PgDn", "page"}, {"g  G", "top / bottom"},
		{"tab ← →  1 2 3", "switch tab"}, {"/", "filter"}, {"esc", "clear filter / back"},
		{"ctrl+r", "refresh now"}, {"mouse", "wheel scroll, click"}, {"q", "quit"},
	}},
	{"Containers", []hint{
		{"enter  l", "follow logs"}, {"d", "inspect details"}, {"s", "start / stop"},
		{"r", "restart"}, {"p", "pause / resume"}, {"i  e", "open shell"},
		{"x", "remove"}, {"y", "copy ID"}, {"a", "all / running only"}, {"o", "cycle sort"},
	}},
	{"Logs", []hint{
		{"↑ ↓  PgUp PgDn", "scroll"}, {"f", "follow on / off"}, {"G", "jump to live"},
		{"w", "wrap lines"}, {"t", "timestamps"}, {"/", "search"}, {"c", "clear"},
		{"r", "reconnect"}, {"esc  q", "back"},
	}},
	{"Images & Volumes", []hint{{"x", "remove"}, {"y", "copy name"}}},
}

func (m *Model) helpBody(w, h int) []string {
	content := m.helpLines(w)
	off := min(m.helpOff, max(0, len(content)-(h-2)))
	right := "? or esc to close"
	if len(content) > h-2 {
		right = "↑↓ scroll · " + right
	}
	lines := []string{spread(bold(cText).Render("Keyboard shortcuts"), fg(cMuted).Render(right), w), ""}
	return append(lines, content[off:]...)
}

// helpLines lays the shortcut sections out in as many columns as fit.
func (m *Model) helpLines(w int) []string {
	const colW = 36
	ncols := max(1, min(len(helpSections), (w+2)/(colW+2)))
	cols := make([][]string, ncols)
	for _, s := range helpSections {
		c := 0
		for i := range cols {
			if len(cols[i]) < len(cols[c]) {
				c = i
			}
		}
		if len(cols[c]) > 0 {
			cols[c] = append(cols[c], "")
		}
		cols[c] = append(cols[c], bold(cPink).Render("▍"+s.title))
		for _, k := range s.keys {
			cols[c] = append(cols[c], "  "+bold(cCyan).Render(fit(k.key, 15))+fg(cText).Render(k.desc))
		}
	}
	rows := 0
	for _, c := range cols {
		rows = max(rows, len(c))
	}
	var lines []string
	for r := 0; r < rows; r++ {
		var b strings.Builder
		for i, c := range cols {
			s := ""
			if r < len(c) {
				s = c[r]
			}
			if i < len(cols)-1 {
				s = fitANSI(s, colW+2)
			}
			b.WriteString(s)
		}
		lines = append(lines, b.String())
	}
	return lines
}

// ---------------------------------------------------------- bottom bars ---

func (m *Model) viewStatus(w int) string {
	if m.filtering {
		q, label := m.filter[m.tab], "filter "+strings.ToLower(m.tab.String())
		if m.screen == screenLogs {
			q, label = m.logs.filter, "search logs"
		}
		return bold(cYellow).Render("/ ") + fg(cText).Render(q) + fg(cPink).Render("█") +
			fg(cMuted).Render("   "+label)
	}
	if t := m.toast; t != nil && time.Now().Before(t.until) {
		switch t.kind {
		case toastOK:
			return bold(cGreen).Render("✓ ") + fg(cText).Render(t.text)
		case toastErr:
			return bold(cRed).Render("✗ ") + fg(cRed).Render(t.text)
		}
		return bold(cCyan).Render("• ") + fg(cText).Render(t.text)
	}

	sep := fg(cDim).Render(" · ")
	var parts []string
	switch {
	case m.screen == screenLogs:
		c := m.logs.target
		parts = []string{bold(cText).Render(c.Name), fg(cPink).Render(c.ShortID()), fg(cMuted).Render(c.Image)}
	case m.screen == screenInspect:
		c := m.inspectTarget
		parts = []string{bold(cText).Render(c.Name), fg(cPink).Render(c.ID), fg(cMuted).Render(c.Image)}
	case m.errs[tabContainers] != nil:
		return fg(cMuted).Render("Waiting for " + m.eng.Name + "…")
	case m.tab == tabContainers:
		c, ok := m.selectedContainer()
		if !ok {
			return ""
		}
		parts = []string{bold(cText).Render(c.Name), fg(cPink).Render(c.ShortID()), fg(cMuted).Render(c.Image)}
		if p := compactPorts(c.Ports); p != "" {
			parts = append(parts, fg(cYellow).Render("⇄ "+p))
		}
		if s, ok := m.stats[c.ShortID()]; ok && c.Running() {
			parts = append(parts,
				fg(cMuted).Render("mem "+s.Mem+" / "+s.MemLimit),
				fg(cMuted).Render("net "+s.NetIO),
				fg(cMuted).Render("disk "+s.BlockIO))
		}
	case m.tab == tabImages:
		vis := m.visibleImages()
		if m.cursor[tabImages] >= len(vis) {
			return ""
		}
		img := vis[m.cursor[tabImages]]
		parts = []string{bold(cText).Render(img.Ref()), fg(cPink).Render(img.ID), fg(cYellow).Render(img.Size), fg(cMuted).Render(img.Created)}
		var users []string
		for _, c := range m.containers {
			ci := strings.TrimPrefix(c.Image, "sha256:")
			if c.Image == img.Ref() || (img.Tag == "latest" && c.Image == img.Repository) ||
				(len(ci) >= 12 && strings.HasPrefix(ci, img.ID)) {
				users = append(users, c.Name)
			}
		}
		if len(users) > 0 {
			parts = append(parts, fg(cGreen).Render("used by "+strings.Join(users, ", ")))
		}
	case m.tab == tabVolumes:
		vis := m.visibleVolumes()
		if m.cursor[tabVolumes] >= len(vis) {
			return ""
		}
		v := vis[m.cursor[tabVolumes]]
		parts = []string{bold(cText).Render(shortName(v.Name)), fg(cCyan).Render(v.Driver), fg(cMuted).Render(v.Mountpoint)}
	}
	return strings.Join(parts, sep)
}

func (m *Model) hints() []hint {
	switch {
	case m.confirm != nil:
		return []hint{{"y", "confirm"}, {"n esc", "cancel"}}
	case m.help:
		return []hint{{"↑↓", "scroll"}, {"esc", "close help"}}
	case m.filtering:
		return []hint{{"enter", "keep filter"}, {"esc", "clear"}, {"↑↓", "move"}}
	case m.screen == screenLogs:
		follow := "pause"
		if !m.logs.follow {
			follow = "follow"
		}
		return []hint{{"↑↓", "scroll"}, {"f", follow}, {"/", "search"}, {"w", "wrap"}, {"t", "time"}, {"d", "inspect"}, {"esc", "back"}}
	case m.screen == screenInspect:
		return []hint{{"↑↓", "scroll"}, {"enter", "logs"}, {"i", "shell"}, {"esc", "back"}}
	case m.tab == tabImages, m.tab == tabVolumes:
		return []hint{{"↑↓", "move"}, {"x", "remove"}, {"y", "copy"}, {"/", "filter"}, {"tab", "switch"}, {"q", "quit"}}
	}
	toggle := "start"
	if c, ok := m.selectedContainer(); ok {
		switch {
		case c.Paused():
			toggle = "resume"
		case c.Running():
			toggle = "stop"
		}
	}
	return []hint{
		{"↑↓", "move"}, {"enter", "logs"}, {"s", toggle}, {"r", "restart"}, {"i", "shell"},
		{"d", "inspect"}, {"x", "remove"}, {"/", "filter"}, {"tab", "switch"}, {"q", "quit"},
	}
}

func (m *Model) viewKeys(w int) string {
	render := func(h hint) string { return bold(cCyan).Render(h.key) + " " + fg(cMuted).Render(h.desc) }
	dot := fg(cDim).Render(" • ")
	tail := ""
	if !m.help && m.confirm == nil {
		tail = render(hint{"?", "help"})
	}
	used := lipgloss.Width(tail)
	var parts []string
	for _, h := range m.hints() {
		r := render(h)
		if used+lipgloss.Width(r)+3 > w {
			break
		}
		parts = append(parts, r)
		used += lipgloss.Width(r) + 3
	}
	if tail != "" {
		parts = append(parts, tail)
	}
	return strings.Join(parts, dot)
}
