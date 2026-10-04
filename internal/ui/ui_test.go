package ui

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"ctui/internal/engine"
)

func testModel(w, h int) *Model {
	m := New(&engine.Engine{Name: "docker"}, Options{Stats: true})
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m.Update(versionMsg("27.1.1"))
	m.Update(containersMsg{list: []engine.Container{
		{ID: "abc123def4567890", Name: "web-1", Image: "nginx:latest", State: "running", Status: "Up 2 hours (healthy)", Ports: "0.0.0.0:8080->80/tcp, :::8080->80/tcp"},
		{ID: "bcd234efa5678901", Name: "api-server-with-a-very-long-name", Image: "ghcr.io/acme/api:1.4.2", State: "running", Status: "Up 5 minutes", Ports: "0.0.0.0:3000->3000/tcp"},
		{ID: "cde345fab6789012", Name: "cache", Image: "redis:7", State: "paused", Status: "Up 1 hour (Paused)"},
		{ID: "def456abc7890123", Name: "old-job", Image: "busybox", State: "exited", Status: "Exited (1) 3 days ago"},
	}})
	m.Update(statsMsg{stats: map[string]engine.Stats{
		"abc123def456": {CPU: 2.5, Mem: "12.4MiB", MemLimit: "7.6GiB", MemPerc: 0.16, NetIO: "1.2kB / 3kB", BlockIO: "0B / 0B"},
		"bcd234efa567": {CPU: 91, Mem: "1.2GiB", MemLimit: "7.6GiB", MemPerc: 15.8, NetIO: "5MB / 1MB", BlockIO: "1MB / 0B"},
	}})
	m.Update(imagesMsg{list: []engine.Image{
		{ID: "111111111111", Repository: "nginx", Tag: "latest", Size: "187MB", Created: "3 weeks ago"},
		{ID: "222222222222", Repository: "<none>", Tag: "<none>", Size: "5MB", Created: "2 days ago"},
	}})
	m.Update(volumesMsg{list: []engine.Volume{
		{Name: "pgdata", Driver: "local", Mountpoint: "/var/lib/docker/volumes/pgdata/_data"},
		{Name: strings.Repeat("ab", 32), Driver: "local", Mountpoint: "/var/lib/docker/volumes/x/_data"},
	}})
	return m
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// checkFrame asserts the frame exactly fills the terminal.
func checkFrame(t *testing.T, name string, m *Model) string {
	t.Helper()
	out := m.View()
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Errorf("%s: %d lines, want %d", name, len(lines), m.height)
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w != m.width {
			t.Errorf("%s: line %d is %d cells wide, want %d: %q", name, i, w, m.width, l)
			break
		}
	}
	return out
}

func TestFramesFitTerminal(t *testing.T) {
	for _, size := range [][2]int{{50, 12}, {80, 24}, {120, 30}, {200, 50}} {
		w, h := size[0], size[1]
		m := testModel(w, h)
		checkFrame(t, "containers", m)

		m.Update(key("?"))
		checkFrame(t, "help", m)
		m.Update(key("esc"))

		m.Update(key("x"))
		checkFrame(t, "confirm", m)
		m.Update(key("n"))

		m.Update(key("2"))
		checkFrame(t, "images", m)
		m.Update(key("3"))
		checkFrame(t, "volumes", m)
		m.Update(key("1"))

		m.screen = screenLogs
		m.logs = logView{target: m.containers[0], follow: true, wrap: true}
		m.appendLogs([]string{"\x1b[32mINFO\x1b[0m started", strings.Repeat("long line ", 40), "ERROR\tboom"})
		checkFrame(t, "logs", m)

		m.screen = screenInspect
		m.inspectTarget = m.containers[0]
		m.details = &engine.Details{ID: "abc123def4567890", Name: "web-1", State: "running", Env: []string{"A=1"}, Ports: []string{"0.0.0.0:8080 → 80/tcp"}}
		checkFrame(t, "inspect", m)
		m.screen = screenList

		m.Update(containersMsg{err: errors.New("failed to connect to the docker API")})
		checkFrame(t, "offline", m)
	}
}

func TestConfirmRunsActionOnlyOnYes(t *testing.T) {
	m := testModel(120, 30)
	m.Update(key("x"))
	if m.confirm == nil {
		t.Fatal("x should ask for confirmation")
	}
	if len(m.pending) != 0 {
		t.Fatal("action must not start before confirming")
	}
	m.Update(key("n"))
	if m.confirm != nil || len(m.pending) != 0 {
		t.Fatal("n should cancel without running the action")
	}
	m.Update(key("x"))
	_, cmd := m.Update(key("y"))
	if cmd == nil || m.pending[m.containers[0].ID] != "remove" {
		t.Fatalf("y should start the remove action, pending=%v", m.pending)
	}
}

func TestFilterAndSelectionSurviveRefresh(t *testing.T) {
	m := testModel(120, 30)
	m.Update(key("/"))
	for _, r := range "cache" {
		m.Update(key(string(r)))
	}
	m.Update(key("enter"))
	if vis := m.visibleContainers(); len(vis) != 1 || vis[0].Name != "cache" {
		t.Fatalf("filter should leave only cache, got %+v", vis)
	}
	m.Update(key("esc"))
	if n := len(m.visibleContainers()); n != 4 {
		t.Fatalf("esc should clear the filter, got %d containers", n)
	}

	m.Update(key("down"))
	m.Update(key("down"))
	sel, _ := m.selectedContainer()
	// A refresh that reorders the list must keep the same container selected.
	cs := append([]engine.Container(nil), m.containers...)
	cs[0], cs[3] = cs[3], cs[0]
	m.Update(containersMsg{list: cs})
	if now, _ := m.selectedContainer(); now.ID != sel.ID {
		t.Fatalf("selection moved from %s to %s", sel.Name, now.Name)
	}
}

func TestLogScrollAndFollow(t *testing.T) {
	m := testModel(80, 24)
	m.screen = screenLogs
	m.logs = logView{target: m.containers[0], follow: true}
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "line")
	}
	m.appendLogs(lines)
	m.Update(key("k"))
	if m.logs.follow || m.logs.off != 1 {
		t.Fatalf("scrolling up should pause following (off=%d)", m.logs.off)
	}
	m.appendLogs([]string{"new"})
	if m.logs.off != 2 {
		t.Fatalf("view should stay put while paused, off=%d", m.logs.off)
	}
	m.Update(key("G"))
	if !m.logs.follow || m.logs.off != 0 {
		t.Fatal("G should jump back to live")
	}
}

func TestLayoutHidesLowPriorityColumns(t *testing.T) {
	cols := []column{{title: "A", min: 10}, {title: "B", min: 10, priority: 1}, {title: "C", min: 10, weight: 1, priority: 2}}
	w := layout(cols, 24) // B is dropped; A + gap + C = 10 + 2 + 12
	if w[0] != 10 || w[1] != 0 || w[2] != 12 {
		t.Fatalf("widths = %v", w)
	}
}

func TestCompactPorts(t *testing.T) {
	got := compactPorts("0.0.0.0:8080->80/tcp, :::8080->80/tcp, 443/tcp")
	if got != "8080→80/tcp, 443/tcp" {
		t.Fatalf("compactPorts = %q", got)
	}
}

func TestSanitize(t *testing.T) {
	if got := sanitize("\x1b[31mred\x1b[0m\tx\r"); got != "red    x" {
		t.Fatalf("sanitize = %q", got)
	}
}

// TestSnapshot writes plain-text renders for eyeballing:
// CTUI_SNAPSHOT=dir go test ./internal/ui -run Snapshot
func TestSnapshot(t *testing.T) {
	dir := os.Getenv("CTUI_SNAPSHOT")
	if dir == "" {
		t.Skip("set CTUI_SNAPSHOT to write snapshots")
	}
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	m := testModel(130, 24)
	var b strings.Builder
	shot := func(name string) {
		b.WriteString("=== " + name + "\n" + ansi.ReplaceAllString(m.View(), "") + "\n\n")
	}
	shot("containers")
	m.Update(key("x"))
	shot("confirm")
	m.Update(key("n"))
	m.Update(key("?"))
	shot("help")
	m.Update(key("esc"))
	m.Update(key("2"))
	shot("images")
	m.Update(key("1"))
	m.screen = screenLogs
	m.logs = logView{target: m.containers[0], follow: true, wrap: true}
	m.logs.stream = &engine.LogStream{}
	m.appendLogs([]string{"2026/10/04 10:00:01 [notice] start worker", "2026/10/04 10:00:02 [warn] slow upstream", "2026/10/04 10:00:03 [error] connect() failed " + strings.Repeat("x", 150)})
	shot("logs")
	m.logs.stream = nil
	m.screen = screenInspect
	m.inspectTarget = m.containers[0]
	m.details = &engine.Details{ID: "abc123def4567890", Name: "web-1", Image: "nginx:latest", State: "running", Health: "healthy", Created: "2026-10-02 10:00:00", RestartPolicy: "unless-stopped", Command: "/docker-entrypoint.sh nginx -g daemon off;", Env: []string{"NGINX_VERSION=1.27", "PATH=/usr/bin"}, Ports: []string{"0.0.0.0:8080 → 80/tcp"}, Networks: []string{"bridge\t172.17.0.2"}}
	shot("inspect")
	m.screen = screenList
	m.Update(containersMsg{err: errors.New("failed to connect to the docker API at npipe:////./pipe/dockerDesktopLinuxEngine; check if the path is correct and if the daemon is running")})
	shot("offline")
	m = testModel(80, 24)
	shot("containers 80x24")
	m.Update(key("?"))
	shot("help 80x24")
	if err := os.WriteFile(filepath.Join(dir, "snapshot.txt"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
