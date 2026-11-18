package ui

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"

	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/charmbracelet/x/ansi"
)

const (
	agentRecent  = 3 * 24 * time.Hour // how long an ended session with every bead closed stays in the strip
	agentRows    = 5                  // lines of agents, each holding up to agentCols
	agentCols    = 3
	agentCellW   = 40 // narrowest cell worth a column
	agentBarCols = 8
	// glyph, bar, count and state tag with its age around the label, counting the spaces between them
	agentCellFixed = 31
)

// agentSummary is one Claude session and how far it got through the beads it claimed.
type agentSummary struct {
	id            string
	closed, total int
	beads         map[string]bool // claimed bead id -> closed
	open          bool            // holds at least one bead that is not closed
	last          time.Time       // newest update on any of its beads
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
				g = &agentSummary{id: s.id, beads: map[string]bool{}}
				g.pane, g.live = a.herdr[s.id]
				by[s.id] = g
			}
			g.total++
			g.beads[is.ID] = is.Closed()
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

// fold adds the kids' beads and last update to g, counting a bead g already holds once.
func (g *agentSummary) fold(kids []agentSummary) {
	g.beads = maps.Clone(g.beads)
	if g.beads == nil {
		g.beads = map[string]bool{}
	}
	for _, k := range kids {
		for id, closed := range k.beads {
			if _, ok := g.beads[id]; ok {
				continue
			}
			g.beads[id] = closed
			g.total++
			if closed {
				g.closed++
			}
		}
		if k.last.After(g.last) {
			g.last = k.last
		}
	}
}

// agentGroup is one block of the strip: a session with the bead-named sessions it orchestrates, or,
// when no orchestrating session is shown, bead-named sessions gathered under their lane or parent bead.
type agentGroup struct {
	head  *agentSummary // nil for a lane or parent group
	label string        // the lane or parent bead, when head is nil
	kids  []agentSummary
}

// sessionBead is the bead a session is named after, for sessions bt dispatch or the work skill started:
// the longest bead id its name contains, so a name holding t-a.1 maps to t-a.1, not t-a.
func (a *App) sessionBead(id string) *bd.Issue {
	_, label, _ := a.sessionBadge(id)
	var best *bd.Issue
	for _, is := range a.snap.Issues {
		if strings.Contains(label, is.ID) && (best == nil || len(is.ID) > len(best.ID)) {
			best = is
		}
	}
	return best
}

// groupAgents gathers each bead-named session under the nearest shown session, not itself bead-named,
// that claimed an ancestor of its bead; failing that under the bead's lane, else its parent bead.
// Blocks keep the order of their first member in list.
func (a *App) groupAgents(list []agentSummary) []agentGroup {
	at := map[string]int{}
	bead := map[string]*bd.Issue{}
	for i, g := range list {
		at[g.id] = i
		bead[g.id] = a.sessionBead(g.id)
	}
	orchestrator := func(is *bd.Issue) string {
		for p := a.snap.ByID[is.Parent]; p != nil; p = a.snap.ByID[p.Parent] {
			for _, s := range sessions(p) {
				if _, ok := at[s.id]; ok && bead[s.id] == nil {
					return s.id
				}
			}
		}
		return ""
	}
	var out []agentGroup
	block := map[string]int{}
	for _, g := range list {
		key, label := g.id, ""
		if is := bead[g.id]; is != nil {
			if o := orchestrator(is); o != "" {
				key = o
			} else if label = cmp.Or(metaString(is, "lane"), is.Parent); label != "" {
				key = "group:" + label
			}
		}
		i, ok := block[key]
		if !ok {
			i = len(out)
			block[key] = i
			grp := agentGroup{label: label}
			if label == "" {
				head := list[at[key]]
				grp.head = &head
			}
			out = append(out, grp)
		}
		if key != g.id {
			out[i].kids = append(out[i].kids, g)
		}
	}
	return out
}

// metaString reads a string metadata key of a bead.
func metaString(is *bd.Issue, key string) string {
	var m map[string]any
	if len(is.Metadata) == 0 || json.Unmarshal(is.Metadata, &m) != nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

// renderAgents draws the strip above Activity's list: agents side by side, as many per line as the
// width allows, each with its closed/assigned progress. Bead-named sessions are not drawn: their
// beads count toward their orchestrator's cell, or toward a lane cell when no orchestrator is shown.
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

	type slot struct {
		g                  agentSummary
		glyph, label, tail string
		st                 int
	}
	var slots []slot
	for _, grp := range a.groupAgents(list) {
		if grp.head == nil {
			g := agentSummary{}
			g.fold(grp.kids)
			slots = append(slots, slot{g: g, glyph: "≡", label: grp.label, tail: age(g.last), st: stDim})
			continue
		}
		g := *grp.head
		g.fold(grp.kids)
		glyph, label, st := a.sessionBadge(g.id)
		tail := age(g.last)
		if g.live {
			kids := 0
			for _, k := range grp.kids {
				if a.busy(k.id) {
					kids++
				}
			}
			glyph, st, tail = a.liveBadge(g.id, kids)
		}
		slots = append(slots, slot{g: g, glyph: glyph, label: label, tail: tail, st: st})
	}
	var rows [][]slot
	for i := 0; i < len(slots); i += cols {
		rows = append(rows, slots[i:min(i+cols, len(slots))])
	}
	more := 0
	if len(rows) > lines {
		more = len(slots) - lines*cols
		rows = rows[:lines]
	}

	lw := 0
	for _, r := range rows {
		for _, c := range r {
			lw = max(lw, ansi.StringWidth(c.label))
		}
	}
	lw = max(min(lw, cw-agentCellFixed-1), 4)
	cw = min(cw, lw+agentCellFixed+2)
	cell := func(c slot) string {
		filled := c.g.closed * agentBarCols / max(c.g.total, 1)
		bar := sOK.Render(strings.Repeat("█", filled)) + sDim.Render(strings.Repeat("░", agentBarCols-filled))
		s := fmt.Sprintf(" %s %s  %s %s  %s", cellStyles[c.st](c.glyph), padRight(ansi.Truncate(c.label, lw, "…"), lw), bar,
			padRight(fmt.Sprintf("%d/%d", c.g.closed, c.g.total), 5), sDim.Render(c.tail))
		return padRight(ansi.Truncate(s, cw, "…"), cw)
	}
	var text []string
	for _, r := range rows {
		var cells []string
		for _, c := range r {
			cells = append(cells, cell(c))
		}
		text = append(text, ansi.Truncate(strings.TrimRight(strings.Join(cells, ""), " "), w, "…"))
	}
	if more > 0 {
		text = append(text, sDim.Render(fmt.Sprintf("   +%d more", more)))
	}
	return strings.Join(text, "\n") + "\n\n"
}
