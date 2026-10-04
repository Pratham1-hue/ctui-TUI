package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/reflow/truncate"
)

// Neon palette. Lipgloss degrades these gracefully on 256 / 16 color terminals.
var (
	cCyan   = lipgloss.Color("#00F0FF")
	cPink   = lipgloss.Color("#FF2BD6")
	cYellow = lipgloss.Color("#FFE600")
	cGreen  = lipgloss.Color("#39FF14")
	cRed    = lipgloss.Color("#FF3B5C")
	cOrange = lipgloss.Color("#FF9F1C")
	cPurple = lipgloss.Color("#B200FF")
	cText   = lipgloss.Color("#E6E6F0")
	cMuted  = lipgloss.Color("#8A8AA3")
	cDim    = lipgloss.Color("#4E4E66")
	cSelBg  = lipgloss.Color("#2E1A47")
	cInk    = lipgloss.Color("#0B0B14")
)

// Styles are built on demand: in lipgloss v0.9 derived styles share state
// with their parent, so package-level styles must never be modified.
func fg(c lipgloss.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

func bold(c lipgloss.Color) lipgloss.Style { return fg(c).Bold(true) }

func badge(fgc, bgc lipgloss.Color) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(fgc).Background(bgc).Bold(true).Padding(0, 1)
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// stateLook returns the icon, label and color for a container state.
func stateLook(state string) (string, string, lipgloss.Color) {
	switch state {
	case "running":
		return "●", "running", cGreen
	case "paused":
		return "‖", "paused", cYellow
	case "restarting":
		return "↻", "restarting", cOrange
	case "created":
		return "◌", "created", cCyan
	case "dead":
		return "✗", "dead", cRed
	case "removing":
		return "…", "removing", cOrange
	case "exited", "stopped":
		return "○", state, cMuted
	default:
		if state == "" {
			state = "unknown"
		}
		return "?", state, cMuted
	}
}

// ---------------------------------------------------------- text fitting ---

// fit truncates plain text to w cells and pads it on the right.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return runewidth.FillRight(runewidth.Truncate(s, w, "…"), w)
}

// fitRight truncates plain text to w cells and pads it on the left.
func fitRight(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return runewidth.FillLeft(runewidth.Truncate(s, w, "…"), w)
}

// fitANSI truncates styled text to w cells and pads it with spaces.
func fitANSI(s string, w int) string {
	if lipgloss.Width(s) > w {
		s = truncate.String(s, uint(w))
	}
	if pad := w - lipgloss.Width(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// spread places left and right on one line of width w, dropping right if
// there is not enough room.
func spread(left, right string, w int) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// center horizontally centers styled text in w cells.
func center(s string, w int) string {
	pad := (w - lipgloss.Width(s)) / 2
	if pad <= 0 {
		return s
	}
	return strings.Repeat(" ", pad) + s
}

func rule(w int) string { return fg(cDim).Render(strings.Repeat("─", w)) }

// wrapPlain hard-wraps plain text into chunks of at most w cells.
func wrapPlain(s string, w int) []string {
	if w <= 0 || runewidth.StringWidth(s) <= w {
		return []string{s}
	}
	var out []string
	var b strings.Builder
	cur := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if cur+rw > w {
			out = append(out, b.String())
			b.Reset()
			cur = 0
		}
		b.WriteRune(r)
		cur += rw
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}
