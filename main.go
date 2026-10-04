// ctui is a tiny, keyboard-driven container dashboard for Windows Terminal.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"ctui/internal/engine"
	"ctui/internal/ui"
)

// version is overridden at build time: -ldflags "-X main.version=1.0.0"
var version = "dev"

func main() {
	runtimeName := flag.String("runtime", os.Getenv("CTUI_RUNTIME"), "container CLI to use: docker or podman (default: auto-detect)")
	refresh := flag.Duration("refresh", 2*time.Second, "how often to refresh the container list")
	tail := flag.Int("tail", 200, "number of log lines to load when opening logs")
	noStats := flag.Bool("no-stats", false, "don't sample live CPU / memory usage")
	noMouse := flag.Bool("no-mouse", false, "disable mouse support (keeps native text selection)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("ctui", version)
		return
	}
	if *refresh < 500*time.Millisecond {
		*refresh = 500 * time.Millisecond
	}

	eng, err := engine.Detect(*runtimeName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ctui:", err)
		os.Exit(1)
	}

	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if !*noMouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	model := ui.New(eng, ui.Options{Refresh: *refresh, Stats: !*noStats, Tail: *tail})
	if _, err := tea.NewProgram(model, opts...).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "ctui:", err)
		os.Exit(1)
	}
}
