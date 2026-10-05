package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
)

// ANSI 16 colours follow the terminal theme, so one palette works on dark and light backgrounds.
var (
	cRed    = lipgloss.Color("1")
	cGreen  = lipgloss.Color("2")
	cYellow = lipgloss.Color("3")
	cBlue   = lipgloss.Color("4")
	cAccent = lipgloss.Color("6")
	cGray   = lipgloss.Color("8")

	sDim     = lipgloss.NewStyle().Faint(true)
	sBold    = lipgloss.NewStyle().Bold(true)
	sAccent  = lipgloss.NewStyle().Foreground(cAccent)
	sWarn    = lipgloss.NewStyle().Foreground(cYellow)
	sErr     = lipgloss.NewStyle().Foreground(cRed)
	sOK      = lipgloss.NewStyle().Foreground(cGreen)
	sRule    = lipgloss.NewStyle().Foreground(cGray)
	sTabOn   = lipgloss.NewStyle().Bold(true).Foreground(cAccent).Underline(true)
	sTabOff  = lipgloss.NewStyle().Faint(true)
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
	switch st {
	case "in_progress":
		return sWarn.Render(g)
	case "blocked":
		return sErr.Render(g)
	case "closed":
		return sOK.Render(g)
	case "deferred":
		return lipgloss.NewStyle().Foreground(cBlue).Render(g)
	}
	return g
}

func prio(p int) string {
	s := fmt.Sprintf("P%d", p)
	switch p {
	case 0:
		return sErr.Bold(true).Render(s)
	case 1:
		return sWarn.Render(s)
	}
	return sDim.Render(s)
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
