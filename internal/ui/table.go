package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const colGap = 2

// column describes one table column. Columns shrink to min and are hidden
// (lowest priority first) when the terminal is too narrow.
type column struct {
	title    string
	min, max int  // max 0 means unbounded
	weight   int  // share of spare width; 0 keeps the column at min
	priority int  // 0 = never hidden; otherwise lower is hidden first
	right    bool // right-align
	off      bool // always hidden
}

// cell is one plain-text value plus how to color it.
type cell struct {
	text  string
	color lipgloss.Color
	bold  bool
}

// layout returns a width per column (0 = hidden) for the available width.
func layout(cols []column, avail int) []int {
	visible := make([]bool, len(cols))
	for i, c := range cols {
		visible[i] = !c.off
	}
	need := func() int {
		n, sum := 0, 0
		for i, c := range cols {
			if visible[i] {
				n++
				sum += c.min
			}
		}
		if n > 1 {
			sum += colGap * (n - 1)
		}
		return sum
	}
	for need() > avail {
		drop := -1
		for i, c := range cols {
			if visible[i] && c.priority > 0 && (drop < 0 || c.priority < cols[drop].priority) {
				drop = i
			}
		}
		if drop < 0 {
			break
		}
		visible[drop] = false
	}

	widths := make([]int, len(cols))
	for i, c := range cols {
		if visible[i] {
			widths[i] = c.min
		}
	}
	extra := avail - need()
	for extra > 0 {
		total := 0
		for i, c := range cols {
			if visible[i] && c.weight > 0 && (c.max == 0 || widths[i] < c.max) {
				total += c.weight
			}
		}
		if total == 0 {
			break
		}
		given := 0
		for i, c := range cols {
			if !visible[i] || c.weight == 0 || (c.max > 0 && widths[i] >= c.max) {
				continue
			}
			share := extra * c.weight / total
			if share == 0 {
				share = 1
			}
			if c.max > 0 && widths[i]+share > c.max {
				share = c.max - widths[i]
			}
			if share > extra-given {
				share = extra - given
			}
			widths[i] += share
			given += share
		}
		if given == 0 {
			break
		}
		extra -= given
	}
	return widths
}

// renderHeader renders the column titles.
func renderHeader(cols []column, widths []int, w int) string {
	var parts []string
	for i, c := range cols {
		if widths[i] == 0 {
			continue
		}
		t := fit(c.title, widths[i])
		if c.right {
			t = fitRight(c.title, widths[i])
		}
		parts = append(parts, t)
	}
	return fitANSI(bold(cCyan).Render("  "+strings.Join(parts, strings.Repeat(" ", colGap))), w)
}

// renderRow renders one table row, w cells wide, with a selection marker.
func renderRow(cols []column, widths []int, cells []cell, selected bool, w int) string {
	base := func() lipgloss.Style {
		s := lipgloss.NewStyle()
		if selected {
			s = s.Background(cSelBg)
		}
		return s
	}
	var b strings.Builder
	if selected {
		b.WriteString(base().Foreground(cPink).Bold(true).Render("▌ "))
	} else {
		b.WriteString("  ")
	}
	used, first := 2, true
	for i, c := range cols {
		if widths[i] == 0 {
			continue
		}
		if !first {
			b.WriteString(base().Render(strings.Repeat(" ", colGap)))
			used += colGap
		}
		first = false
		var v cell
		if i < len(cells) {
			v = cells[i]
		}
		text := fit(v.text, widths[i])
		if c.right {
			text = fitRight(v.text, widths[i])
		}
		color := v.color
		if color == "" {
			color = cText
		}
		st := base().Foreground(color)
		if v.bold {
			st = st.Bold(true)
		}
		b.WriteString(st.Render(text))
		used += widths[i]
	}
	if pad := w - used; pad > 0 {
		b.WriteString(base().Render(strings.Repeat(" ", pad)))
	}
	return fitANSI(b.String(), w)
}

// scrollWindow returns the first visible index so that cursor stays within
// a window of h rows.
func scrollWindow(cursor, offset, h, n int) int {
	if h <= 0 || n <= h {
		return 0
	}
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+h {
		offset = cursor - h + 1
	}
	if offset > n-h {
		offset = n - h
	}
	if offset < 0 {
		offset = 0
	}
	return offset
}
