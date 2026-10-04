package ui

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/aymanbagabas/go-osc52/v2"
	tea "github.com/charmbracelet/bubbletea"

	"ctui/internal/engine"
)

type (
	tickMsg       time.Time
	spinMsg       struct{}
	statsTickMsg  struct{}
	versionMsg    string
	containersMsg struct {
		list []engine.Container
		err  error
	}
	imagesMsg struct {
		list []engine.Image
		err  error
	}
	volumesMsg struct {
		list []engine.Volume
		err  error
	}
	statsMsg struct {
		stats map[string]engine.Stats
		err   error
	}
	actionMsg struct {
		key, name, verb string
		tab             tab
		err             error
	}
	logStartedMsg struct {
		gen    int
		stream *engine.LogStream
		err    error
	}
	logBatchMsg struct {
		stream *engine.LogStream
		lines  []string
		closed bool
	}
	detailsMsg struct {
		id      string
		details *engine.Details
		err     error
	}
	shellReadyMsg struct {
		name string
		cmd  *exec.Cmd
	}
	shellDoneMsg struct {
		name string
		err  error
	}
	clipboardMsg struct {
		text string
		err  error
	}
)

func withTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

func (m *Model) tick() tea.Cmd {
	return tea.Tick(m.opts.Refresh, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func spinCmd() tea.Cmd {
	return tea.Tick(90*time.Millisecond, func(time.Time) tea.Msg { return spinMsg{} })
}

// startSpin starts the spinner loop unless it is already running.
func (m *Model) startSpin() tea.Cmd {
	if m.spinning {
		return nil
	}
	m.spinning = true
	return spinCmd()
}

// fetch reloads one list in the background. Overlapping fetches are skipped.
func (m *Model) fetch(t tab) tea.Cmd {
	if m.fetching[t] {
		return nil
	}
	m.fetching[t] = true
	eng := m.eng
	return tea.Batch(m.startSpin(), func() tea.Msg {
		ctx, cancel := withTimeout(15 * time.Second)
		defer cancel()
		switch t {
		case tabImages:
			l, err := eng.Images(ctx)
			return imagesMsg{l, err}
		case tabVolumes:
			l, err := eng.Volumes(ctx)
			return volumesMsg{l, err}
		default:
			l, err := eng.List(ctx, true)
			return containersMsg{l, err}
		}
	})
}

func (m *Model) versionCmd() tea.Cmd {
	eng := m.eng
	return func() tea.Msg {
		ctx, cancel := withTimeout(10 * time.Second)
		defer cancel()
		return versionMsg(eng.ServerVersion(ctx))
	}
}

func (m *Model) statsCmd() tea.Cmd {
	eng := m.eng
	return func() tea.Msg {
		ctx, cancel := withTimeout(20 * time.Second)
		defer cancel()
		s, err := eng.Stats(ctx)
		return statsMsg{s, err}
	}
}

// statsLater schedules the next stats sample. `stats --no-stream` itself
// takes a moment, so samples are never closer than two seconds apart.
func (m *Model) statsLater() tea.Cmd {
	d := max(m.opts.Refresh, 2*time.Second)
	return tea.Tick(d, func(time.Time) tea.Msg { return statsTickMsg{} })
}

// act runs a lifecycle action in the background. key identifies the item
// (to show a spinner on its row); fn receives arg, or key if arg is empty.
func (m *Model) act(key, name, verb string, t tab, fn func(context.Context, string) error, arg ...string) tea.Cmd {
	if _, busy := m.pending[key]; busy {
		return nil
	}
	target := key
	if len(arg) > 0 {
		target = arg[0]
	}
	m.pending[key] = verb
	return tea.Batch(m.startSpin(), func() tea.Msg {
		ctx, cancel := withTimeout(2 * time.Minute)
		defer cancel()
		return actionMsg{key: key, name: name, verb: verb, tab: t, err: fn(ctx, target)}
	})
}

func (m *Model) shell(c engine.Container) tea.Cmd {
	eng, id, name := m.eng, c.ID, c.Name
	return func() tea.Msg {
		ctx, cancel := withTimeout(10 * time.Second)
		defer cancel()
		return shellReadyMsg{name: name, cmd: eng.ShellCommand(ctx, id)}
	}
}

func (m *Model) openInspect(c engine.Container) tea.Cmd {
	m.closeLogs()
	m.screen = screenInspect
	m.inspectTarget, m.details, m.detailsErr, m.inspectOff = c, nil, nil, 0
	eng, id := m.eng, c.ID
	return tea.Batch(m.startSpin(), func() tea.Msg {
		ctx, cancel := withTimeout(15 * time.Second)
		defer cancel()
		d, err := eng.Inspect(ctx, id)
		return detailsMsg{id, d, err}
	})
}

// ------------------------------------------------------------------ logs ---

func (m *Model) openLogs(c engine.Container) tea.Cmd {
	m.closeLogs()
	m.screen = screenLogs
	m.filtering = false
	m.logs = logView{target: c, follow: true, wrap: m.logs.wrap, timestamps: m.logs.timestamps, gen: m.logs.gen}
	return m.startLogs()
}

func (m *Model) reconnectLogs() tea.Cmd {
	m.closeLogs()
	m.logs.lines, m.logs.off, m.logs.follow = nil, 0, true
	m.logs.ended, m.logs.err = false, nil
	return m.startLogs()
}

func (m *Model) startLogs() tea.Cmd {
	m.logs.gen++
	gen, id, tail, ts, eng := m.logs.gen, m.logs.target.ID, m.opts.Tail, m.logs.timestamps, m.eng
	return tea.Batch(m.startSpin(), func() tea.Msg {
		s, err := eng.StreamLogs(id, tail, ts)
		return logStartedMsg{gen: gen, stream: s, err: err}
	})
}

func (m *Model) closeLogs() {
	if m.logs.stream != nil {
		m.logs.stream.Close()
		m.logs.stream = nil
	}
	m.logs.gen++
}

// readLogs waits for the next log line, then drains whatever else is
// already buffered so bursts are rendered in one frame.
func readLogs(s *engine.LogStream) tea.Cmd {
	return func() tea.Msg {
		line, ok := <-s.Lines
		if !ok {
			return logBatchMsg{stream: s, closed: true}
		}
		lines := []string{line}
		for len(lines) < 1000 {
			select {
			case l, ok := <-s.Lines:
				if !ok {
					return logBatchMsg{stream: s, lines: lines, closed: true}
				}
				lines = append(lines, l)
			default:
				return logBatchMsg{stream: s, lines: lines}
			}
		}
		return logBatchMsg{stream: s, lines: lines}
	}
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[@-Z\\-_]`)

// sanitize strips terminal escapes and control characters from a log line.
func sanitize(s string) string {
	s = ansiRE.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\t", "    ")
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

func (m *Model) appendLogs(lines []string) {
	q := strings.ToLower(m.logs.filter)
	added := 0
	for _, l := range lines {
		l = sanitize(l)
		m.logs.lines = append(m.logs.lines, l)
		if q == "" || strings.Contains(strings.ToLower(l), q) {
			added++
		}
	}
	if !m.logs.follow {
		m.logs.off += added // keep the view still while paused
	}
	if over := len(m.logs.lines) - maxLogLines; over > 0 {
		m.logs.lines = append([]string(nil), m.logs.lines[over:]...)
	}
	m.clampLogs()
}

// logLines returns the log lines that pass the filter.
func (m *Model) logLines() []string {
	q := strings.ToLower(m.logs.filter)
	if q == "" {
		return m.logs.lines
	}
	var out []string
	for _, l := range m.logs.lines {
		if strings.Contains(strings.ToLower(l), q) {
			out = append(out, l)
		}
	}
	return out
}

func (m *Model) logViewRows() int { return max(1, m.bodyRows()-1) }

// maxLogOff is how far the log view can scroll up while staying full.
func (m *Model) maxLogOff() int {
	src, h := m.logLines(), m.logViewRows()
	if !m.logs.wrap {
		return max(0, len(src)-h)
	}
	rows, w := 0, m.width-4
	for i, l := range src {
		rows += len(wrapPlain(l, w))
		if rows >= h {
			return max(0, len(src)-(i+1))
		}
	}
	return 0
}

func (m *Model) scrollLogs(up int) {
	m.logs.off += up
	if up > 0 {
		m.logs.follow = false
	}
	m.clampLogs()
	if m.logs.off == 0 && up < 0 {
		m.logs.follow = true
	}
}

func (m *Model) clampLogs() {
	m.logs.off = min(max(0, m.logs.off), m.maxLogOff())
}

func (m *Model) scrollHelp(d int) {
	maxOff := max(0, len(m.helpLines(m.width-4))-(m.bodyRows()-2))
	m.helpOff = min(max(0, m.helpOff+d), maxOff)
}

func (m *Model) scrollInspect(d int) {
	maxOff := max(0, len(m.inspectLines())-(m.bodyRows()-1))
	m.inspectOff = min(max(0, m.inspectOff+d), maxOff)
}

// ------------------------------------------------------------- clipboard ---

func copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		var err error
		if runtime.GOOS == "windows" {
			cmd := exec.Command("clip")
			cmd.Stdin = strings.NewReader(text)
			err = cmd.Run()
		} else {
			_, err = osc52.New(text).WriteTo(os.Stderr)
		}
		return clipboardMsg{text: shortName(text), err: err}
	}
}
