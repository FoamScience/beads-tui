package ui

import (
	"cmp"
	"encoding/json"
	"fmt"
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

// agentGroup is one block of the strip: a session with the bead-named sessions it orchestrates, or,
// when no orchestrating session is shown, bead-named sessions gathered under their lane or parent bead.
type agentGroup struct {
	head  *agentSummary // nil for a lane or parent group
	label string        // the lane or parent bead, when head is nil
	kids  []agentSummary
}

// sessionBead is the bead a session is named after, for sessions bt dispatch or the work skill started.
func (a *App) sessionBead(id string) *bd.Issue {
	_, label, _ := a.sessionBadge(id)
	return a.snap.ByID[label]
}

// groupAgents gathers each bead-named session under the nearest shown session, not itself bead-named,
// that claimed an ancestor of its bead; failing that under the bead's lane, else its parent bead.
// Blocks keep the order of their first member in list.
func (a *App) groupAgents(list []agentSummary) []agentGroup {
	at := map[string]int{}
	for i, g := range list {
		at[g.id] = i
	}
	orchestrator := func(is *bd.Issue) string {
		for p := a.snap.ByID[is.Parent]; p != nil; p = a.snap.ByID[p.Parent] {
			for _, s := range sessions(p) {
				if _, ok := at[s.id]; ok && a.sessionBead(s.id) == nil {
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
		if is := a.sessionBead(g.id); is != nil {
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
// width allows, each with its closed/assigned progress. Bead-named sessions sit in a block led by
// their orchestrator (or lane), wrapping in the columns right of it.
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

	// a slot is one cell: a session, a lane group's head or a "+N done" note
	type slot struct {
		g            agentSummary
		glyph, label string
		st           int
		note         string
		n            int // sessions behind it, for "+N more"
	}
	type row struct {
		cells  []slot
		indent bool
	}
	session := func(g agentSummary) slot {
		glyph, label, st := a.sessionBadge(g.id)
		return slot{g: g, glyph: glyph, label: label, st: st, n: 1}
	}
	head := func(grp agentGroup) slot {
		if grp.head != nil {
			return session(*grp.head)
		}
		sum := agentSummary{}
		for _, k := range grp.kids {
			sum.closed += k.closed
			sum.total += k.total
			if k.last.After(sum.last) {
				sum.last = k.last
			}
		}
		return slot{g: sum, glyph: "≡", label: grp.label, st: stDim}
	}

	var rows []row
	groups := a.groupAgents(list)
	used := make([]bool, len(groups))
	for i, grp := range groups {
		if used[i] {
			continue
		}
		if len(grp.kids) == 0 {
			// a line of lone sessions takes the next lone ones, even past groups, so none is left half empty
			var r row
			for j := i; j < len(groups) && len(r.cells) < cols; j++ {
				if !used[j] && len(groups[j].kids) == 0 {
					used[j] = true
					r.cells = append(r.cells, session(*groups[j].head))
				}
			}
			rows = append(rows, r)
			continue
		}
		used[i] = true
		// kids that ended with every bead closed fold into one cell, so live work stays in view
		var kids []slot
		done := 0
		for _, k := range grp.kids {
			if k.live || k.open {
				kids = append(kids, session(k))
			} else {
				done++
			}
		}
		if done > 0 {
			kids = append(kids, slot{note: fmt.Sprintf("+%d done", done), n: done})
		}
		// the head's line holds what fits beside it; the rest wrap indented across every column
		first := min(cols-1, len(kids))
		rows = append(rows, row{cells: append([]slot{head(grp)}, kids[:first]...)})
		for i := first; i < len(kids); i += cols {
			rows = append(rows, row{cells: kids[i:min(i+cols, len(kids))], indent: true})
		}
	}

	more := 0
	if len(rows) > lines {
		for _, r := range rows[lines:] {
			for _, c := range r.cells {
				more += c.n
			}
		}
		rows = rows[:lines]
	}

	lw := 0
	for _, r := range rows {
		for _, c := range r.cells {
			lw = max(lw, ansi.StringWidth(c.label))
		}
	}
	lw = max(min(lw, cw-agentCellFixed-1), 4)
	cw = min(cw, lw+agentCellFixed+2)
	// cut narrows a cell, so the first cell of an indented line keeps its bar under the bars above
	cell := func(c slot, cut int) string {
		if c.note != "" {
			return padRight(sDim.Render("   "+c.note), cw-cut)
		}
		filled := c.g.closed * agentBarCols / max(c.g.total, 1)
		bar := sOK.Render(strings.Repeat("█", filled)) + sDim.Render(strings.Repeat("░", agentBarCols-filled))
		s := fmt.Sprintf(" %s %s  %s %s  %s", cellStyles[c.st](c.glyph), padRight(ansi.Truncate(c.label, lw-cut, "…"), lw-cut), bar,
			padRight(fmt.Sprintf("%d/%d", c.g.closed, c.g.total), 5), sDim.Render(age(c.g.last)))
		return padRight(ansi.Truncate(s, cw-cut, "…"), cw-cut)
	}
	var text []string
	for _, r := range rows {
		var cells []string
		for i, c := range r.cells {
			cut := 0
			if r.indent && i == 0 {
				cut = 2
			}
			cells = append(cells, cell(c, cut))
		}
		l := strings.TrimRight(strings.Join(cells, ""), " ")
		if r.indent {
			l = "  " + l
		}
		text = append(text, ansi.Truncate(l, w, "…"))
	}
	if more > 0 {
		text = append(text, sDim.Render(fmt.Sprintf("   +%d more", more)))
	}
	return strings.Join(text, "\n") + "\n\n"
}
