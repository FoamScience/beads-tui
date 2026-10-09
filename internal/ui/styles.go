package ui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
)

// ANSI 16 colours follow the terminal theme, so one palette works on dark and light backgrounds.
var (
	cRed     = lipgloss.Color("1")
	cGreen   = lipgloss.Color("2")
	cYellow  = lipgloss.Color("3")
	cBlue    = lipgloss.Color("4")
	cMagenta = lipgloss.Color("5")
	cAccent  = lipgloss.Color("6")
	cGray    = lipgloss.Color("8")

	sDim    = lipgloss.NewStyle().Faint(true)
	sBold   = lipgloss.NewStyle().Bold(true)
	sAccent = lipgloss.NewStyle().Foreground(cAccent)
	sWarn   = lipgloss.NewStyle().Foreground(cYellow)
	sErr    = lipgloss.NewStyle().Foreground(cRed)
	sOK     = lipgloss.NewStyle().Foreground(cGreen)
	sRule   = lipgloss.NewStyle().Foreground(cGray)
	// badges: reversed colours give a background block that follows the terminal theme
	sTabOn   = lipgloss.NewStyle().Bold(true).Foreground(cAccent).Reverse(true).Padding(0, 1)
	sTabOff  = lipgloss.NewStyle().Faint(true).Padding(0, 1)
	sEpic    = lipgloss.NewStyle().Bold(true).Foreground(cMagenta).Reverse(true)
	sLabel   = lipgloss.NewStyle().Foreground(cGray).Reverse(true).Padding(0, 1)
	sKey     = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sSection = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	sSel     = lipgloss.NewStyle().Bold(true)
	sChipOn  = lipgloss.NewStyle().Foreground(cAccent)
	sChipOff = lipgloss.NewStyle().Faint(true).Strikethrough(true)
)

var statusGlyph = map[string]string{
	"open":        "○",
	"in_progress": "◐",
	"blocked":     "●",
	"closed":      "✓",
	"deferred":    "❄",
	"pinned":      "◆",
	"hooked":      "↪",
}

var statuses = []string{"open", "in_progress", "blocked", "deferred", "pinned", "hooked", "closed"}

func glyph(s *bd.Snapshot, is *bd.Issue) string {
	st := is.Status
	if st == "open" && s.IsBlocked(is) {
		st = "blocked"
	}
	g := statusGlyph[st]
	if g == "" {
		g = "?"
	}
	if c := statusColor(s, is); c != cGray {
		return lipgloss.NewStyle().Foreground(c).Render(g)
	}
	return g // open and unblocked stays in the terminal's default colour
}

// prio is a two-cell chip so list columns stay aligned.
func prio(p int) string {
	s := fmt.Sprintf("P%d", p)
	c := cGray
	switch p {
	case 0:
		c = cRed
	case 1:
		c = cYellow
	}
	return lipgloss.NewStyle().Bold(p <= 1).Foreground(c).Reverse(true).Render(s)
}

// statusColor matches the colours glyph uses, so a symbol chip reads the same as a plain glyph.
func statusColor(s *bd.Snapshot, is *bd.Issue) color.Color {
	switch {
	case is.Status == "in_progress":
		return cYellow
	case is.Closed():
		return cGreen
	case is.Status == "deferred":
		return cBlue
	case is.Status == "blocked" || (is.Status == "open" && s.IsBlocked(is)):
		return cRed
	}
	return cGray
}

// epicBadge keeps the row order of every issue (symbol, id, priority, title): the id sits on a
// status-coloured chip and the title on its own chip.
func epicBadge(a *App, is *bd.Issue) string {
	id := lipgloss.NewStyle().Bold(true).Foreground(statusColor(a.snap, is)).Reverse(true).Render(" " + a.shortID(is.ID) + " ")
	return glyph(a.snap, is) + " " + id + " " + prio(is.Priority) + " " + sEpic.Render(" "+is.Title+" ")
}

func labelChips(ls []string) string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = sLabel.Render(l)
	}
	return strings.Join(out, " ")
}

func age(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	return fmt.Sprintf("%dmo", int(d.Hours()/24/30))
}

func minutes(m int) string {
	switch {
	case m <= 0:
		return "—"
	case m < 60:
		return fmt.Sprintf("%dm", m)
	case m%60 == 0:
		return fmt.Sprintf("%dh", m/60)
	}
	return fmt.Sprintf("%.1fh", float64(m)/60)
}

func progressBar(done, total, width int) string {
	if total == 0 || width <= 0 {
		return ""
	}
	n := done * width / total
	return sAccent.Render(strings.Repeat("▰", n)) + sDim.Render(strings.Repeat("▱", width-n))
}

func rule(w int) string { return sRule.Render(strings.Repeat("─", max(w, 0))) }
