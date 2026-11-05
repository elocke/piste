// Package ui holds piste's terminal styling (lipgloss). Colour
// is off for NO_COLOR, non-TTY output and --json.
package ui

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/mattn/go-isatty"
)

// Styles for one output stream.
type Styles struct {
	OK, Warn, Err, Dim, Bold lipgloss.Style
	color                    bool
}

// For returns styles for w: plain unless w is a terminal and NO_COLOR is unset.
func For(w io.Writer) Styles {
	color := false
	if f, ok := w.(*os.File); ok && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb" {
		color = isatty.IsTerminal(f.Fd())
	}
	s := Styles{color: color}
	if !color {
		plain := lipgloss.NewStyle()
		s.OK, s.Warn, s.Err, s.Dim, s.Bold = plain, plain, plain, plain, plain
		return s
	}
	s.OK = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	s.Warn = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	s.Err = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	s.Dim = lipgloss.NewStyle().Faint(true)
	s.Bold = lipgloss.NewStyle().Bold(true)
	return s
}

// Color reports whether styling is on.
func (s Styles) Color() bool { return s.color }

// Width is the console width: $COLUMNS when set, else
// the size of the first of stdin/stdout/stderr that is a terminal, else 80.
func Width() int {
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		return n
	}
	for _, fd := range []uintptr{os.Stdin.Fd(), os.Stdout.Fd(), os.Stderr.Fd()} {
		if w, _, err := term.GetSize(fd); err == nil && w > 0 {
			return w
		}
	}
	return 80
}

// Fit wraps s at spaces to at most width cells, cutting a word that alone
// overflows the line with "…" (unlike ansi.Wordwrap it does not break at
// hyphens). ANSI styling is preserved.
func Fit(s string, width int) []string {
	width = max(width, 1)
	var lines []string
	line := ""
	for _, word := range strings.Split(s, " ") {
		switch {
		case line == "":
			line = word
		case ansi.StringWidth(line)+1+ansi.StringWidth(word) <= width:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	lines = append(lines, line)
	for i, l := range lines {
		if ansi.StringWidth(l) > width {
			lines[i] = ansi.Truncate(l, width, "…")
		}
	}
	return lines
}

// Table renders rows with a header. Plain output is space-aligned (no box
// drawing) so it greps and diffs well.
func (s Styles) Table(header []string, rows [][]string) string {
	if !s.color {
		widths := make([]int, len(header))
		all := append([][]string{header}, rows...)
		for _, r := range all {
			for i, c := range r {
				if i < len(widths) && len(c) > widths[i] {
					widths[i] = len(c)
				}
			}
		}
		var b strings.Builder
		for _, r := range all {
			for i, c := range r {
				if i == len(r)-1 {
					b.WriteString(c)
				} else {
					fmt.Fprintf(&b, "%-*s  ", widths[i], c)
				}
			}
			b.WriteString("\n")
		}
		return b.String()
	}
	t := table.New().Headers(header...).Rows(rows...).
		Border(lipgloss.NormalBorder()).
		BorderStyle(s.Dim)
	return t.Render() + "\n"
}

// Panel frames body with an optional title, as wide as its content.
func (s Styles) Panel(title, body string) string {
	lines := strings.Split(body, "\n")
	w := 0
	for _, l := range lines {
		if n := lipgloss.Width(l); n > w {
			w = n
		}
	}
	if n := lipgloss.Width(title) + 2; n > w {
		w = n
	}
	var b strings.Builder
	top := "╭" + strings.Repeat("─", w+2) + "╮"
	if title != "" {
		t := " " + s.Bold.Render(title) + " "
		left := (w + 2 - lipgloss.Width(t)) / 2
		top = "╭" + strings.Repeat("─", left) + t + strings.Repeat("─", w+2-left-lipgloss.Width(t)) + "╮"
	}
	b.WriteString(top + "\n")
	for _, l := range lines {
		b.WriteString("│ " + l + strings.Repeat(" ", w-lipgloss.Width(l)) + " │\n")
	}
	b.WriteString("╰" + strings.Repeat("─", w+2) + "╯\n")
	return b.String()
}
