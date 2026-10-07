package ui

import "strings"

var helpSections = []struct {
	title string
	keys  []string
}{
	{"Navigate", []string{"1-9 view", "b board", "tab next view", "j/k move", "ctrl+d/u half page", "[/] prev/next group", "home/end top/bottom", "enter detail", "esc back", "/ search all", "r reload", "q quit"}},
	{"Act on selection", []string{"s status", "C claim", "c close", "n note", "p priority", "l label", "m machine", "x external ref", "e estimate", "d defer", "a new child", "y yank id", "o open ref", "S bd sync"}},
	{"Views", []string{"f filter (Now, Ready)", "M machine filter", "z expand epic", "space mark (bulk actions)", "esc clear marks", "i inbox", "R resume agent", "L swarm lint", "H show closed", "Z fold text (detail)", "tab/S-tab next/prev link (detail)", "enter open link, esc back"}},
}

func (a *App) helpView(h int) string {
	var cols []string
	for _, s := range helpSections {
		var b strings.Builder
		b.WriteString(" " + sSection.Render(s.title) + "\n\n")
		for _, k := range s.keys {
			key, rest, _ := strings.Cut(k, " ")
			b.WriteString(" " + padRight(sKey.Render(key), 10) + rest + "\n")
		}
		cols = append(cols, b.String())
	}
	if a.w < 100 {
		return strings.Join(cols, "\n")
	}
	return joinCols(cols, a.w/len(cols))
}

func joinCols(cols []string, w int) string {
	split := make([][]string, len(cols))
	n := 0
	for i, c := range cols {
		split[i] = strings.Split(c, "\n")
		n = max(n, len(split[i]))
	}
	var b strings.Builder
	for r := 0; r < n; r++ {
		for i := range split {
			cell := ""
			if r < len(split[i]) {
				cell = split[i][r]
			}
			b.WriteString(padRight(cell, w))
		}
		b.WriteByte('\n')
	}
	return b.String()
}
