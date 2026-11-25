package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// card is a rounded box with its title set into the top edge and chips flowed inside.
type card struct {
	title string
	chips []string
}

const (
	cardGap      = 1
	cardMinInner = 24
)

// cards lays the cards out side by side across w, splitting the width evenly, or stacks them
// full width when a share would drop below cardMinInner. Each box keeps at most maxH rows.
func cards(w, maxH int, cs ...card) []string {
	n := len(cs)
	if n == 0 {
		return nil
	}
	inner := (w-cardGap*(n-1))/n - 4
	if n > 1 && inner < cardMinInner {
		var out []string
		for _, c := range cs {
			out = append(out, cards(w, maxH, c)...)
		}
		return out
	}
	bodies := make([][]string, n)
	rows := 0
	for i, c := range cs {
		bodies[i] = flowChips(inner, c.chips...)
		rows = max(rows, len(bodies[i]))
	}
	rows = clamp(rows, 1, max(maxH-2, 1))
	boxes := make([][]string, n)
	for i, c := range cs {
		iw := inner
		if i == n-1 {
			iw = w - (inner+4+cardGap)*(n-1) - 4 // the last box takes the rounding remainder
		}
		boxes[i] = box(c.title, bodies[i], iw, rows)
	}
	out := make([]string, rows+2)
	for r := range out {
		parts := make([]string, n)
		for i := range boxes {
			parts[i] = boxes[i][r]
		}
		out[r] = strings.Join(parts, strings.Repeat(" ", cardGap))
	}
	return out
}

func box(title string, body []string, inner, rows int) []string {
	title = ansi.Truncate(title, max(inner-2, 1), "…")
	top := sRule.Render("╭" + strings.Repeat("─", inner+2) + "╮")
	if title != "" {
		top = sRule.Render("╭─ ") + title + " " + sRule.Render(strings.Repeat("─", max(inner-lipgloss.Width(title)-1, 0))+"╮")
	}
	out := []string{top}
	for r := range rows {
		line := ""
		if r < len(body) {
			line = ansi.Truncate(body[r], inner, "…")
		}
		out = append(out, sRule.Render("│")+" "+padRight(line, inner)+" "+sRule.Render("│"))
	}
	return append(out, sRule.Render("╰"+strings.Repeat("─", inner+2)+"╯"))
}

// flowChips lays chips out left to right, starting a new line when the next one would pass w.
func flowChips(w int, chips ...string) []string {
	const sep = "   "
	var lines []string
	cur := ""
	for _, c := range chips {
		if cur != "" && lipgloss.Width(cur)+len(sep)+lipgloss.Width(c) > w {
			lines = append(lines, cur)
			cur = ""
		}
		if cur == "" {
			cur = c
		} else {
			cur += sep + c
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// header is the card block every tab opens with: the machine filter in the title edge.
func header(a *App, w int, chips ...string) []string {
	return cards(w, 3, card{machineChip(a), chips})
}
