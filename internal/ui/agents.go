package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

const (
	agentRecent  = 3 * 24 * time.Hour // how long an ended session with every bead closed stays in the strip
	agentRows    = 5                  // lines of agents, each holding up to agentCols
	agentCols    = 3
	agentCellW   = 40 // narrowest cell worth a column
	agentBarCols = 8
	// glyph, bar, count and age around the label, counting the spaces between them
	agentCellFixed = 25
)

// agentSummary is one Claude session and how far it got through the beads it claimed.
type agentSummary struct {
	id            string
	closed, total int
	open          bool      // holds at least one bead that is not closed
	last          time.Time // newest update on any of its beads
	pane          agentPane
	live          bool
}

// agents summarises the sessions recorded on beads: live ones first, then ended ones still holding
// an unclosed bead or touched recently.
func (a *App) agents() []agentSummary {
	by := map[string]*agentSummary{}
	for _, is := range a.snap.Issues {
		if !a.machineMatch(is) {
			continue
		}
		for _, s := range sessions(is) {
			g := by[s.id]
			if g == nil {
				g = &agentSummary{id: s.id}
				g.pane, g.live = a.herdr[s.id]
				by[s.id] = g
			}
			g.total++
			if is.Closed() {
				g.closed++
			} else {
				g.open = true
			}
			if is.UpdatedAt.After(g.last) {
				g.last = is.UpdatedAt
			}
		}
	}
	var out []agentSummary
	for _, g := range by {
		if g.live || g.open || time.Since(g.last) < agentRecent {
			out = append(out, *g)
		}
	}
	rank := func(g agentSummary) int {
		switch {
		case g.live && g.pane.Status == "working":
			return 0
		case g.live:
			return 1
		}
		return 2
	}
	sort.Slice(out, func(i, j int) bool {
		if ri, rj := rank(out[i]), rank(out[j]); ri != rj {
			return ri < rj
		}
		if !out[i].last.Equal(out[j].last) {
			return out[i].last.After(out[j].last)
		}
		return out[i].id < out[j].id
	})
	return out
}

// renderAgents draws the strip above Activity's list: agents side by side, as many per line as the
// width allows, each with its closed/assigned progress.
func (a *App) renderAgents(w, h int) string {
	if a.herdr == nil {
		return ""
	}
	list := a.agents()
	lines := min(agentRows, h/5)
	if len(list) == 0 || lines < 1 {
		return ""
	}
	cols := max(min(w/agentCellW, agentCols), 1)
	cw := w / cols
	shown := min(len(list), lines*cols)
	more := len(list) - shown
	list = list[:shown]

	lw := 0
	for _, g := range list {
		_, label, _ := a.sessionBadge(g.id)
		lw = max(lw, ansi.StringWidth(label))
	}
	lw = max(min(lw, cw-agentCellFixed-1), 4)

	cell := func(g agentSummary) string {
		glyph, label, st := a.sessionBadge(g.id)
		filled := g.closed * agentBarCols / g.total
		bar := sOK.Render(strings.Repeat("█", filled)) + sDim.Render(strings.Repeat("░", agentBarCols-filled))
		c := fmt.Sprintf(" %s %s  %s %s  %s", cellStyles[st](glyph), padRight(ansi.Truncate(label, lw, "…"), lw), bar,
			padRight(fmt.Sprintf("%d/%d", g.closed, g.total), 5), sDim.Render(age(g.last)))
		return padRight(ansi.Truncate(c, cw, "…"), cw)
	}
	var out []string
	for i := 0; i < len(list); i += cols {
		var cells []string
		for _, g := range list[i:min(i+cols, len(list))] {
			cells = append(cells, cell(g))
		}
		out = append(out, ansi.Truncate(strings.TrimRight(strings.Join(cells, ""), " "), w, "…"))
	}
	if more > 0 {
		out = append(out, sDim.Render(fmt.Sprintf("   +%d more", more)))
	}
	return strings.Join(out, "\n") + "\n\n"
}
