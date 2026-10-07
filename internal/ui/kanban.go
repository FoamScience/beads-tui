package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/charmbracelet/x/ansi"
)

// kanbanColumn is one lane of the board; status is what moving a card into it sets.
type kanbanColumn struct {
	name   string
	status string
	has    func(s *bd.Snapshot, is *bd.Issue) bool
}

const doneWindow = 7 * 24 * time.Hour

var kanbanColumns = []kanbanColumn{
	{"Ready", "open", func(s *bd.Snapshot, is *bd.Issue) bool { return ready(s, is) }},
	{"Blocked", "blocked", func(s *bd.Snapshot, is *bd.Issue) bool {
		return is.Status == "blocked" || (is.Status == "open" && len(s.BlockChain(is)) > 0)
	}},
	{"In progress", "in_progress", func(s *bd.Snapshot, is *bd.Issue) bool {
		return is.Status == "in_progress" || is.Status == "pinned" || is.Status == "hooked"
	}},
	{"Deferred", "deferred", func(s *bd.Snapshot, is *bd.Issue) bool { return is.Status == "deferred" }},
	{"Done", "closed", func(s *bd.Snapshot, is *bd.Issue) bool {
		return is.Closed() && is.ClosedAt != nil && time.Since(*is.ClosedAt) < doneWindow
	}},
}

type kanbanView struct {
	cards  [][]*bd.Issue
	col    int
	row    []int // selected card per column
	offset []int // first visible card per column
}

func newKanban() *kanbanView {
	n := len(kanbanColumns)
	return &kanbanView{cards: make([][]*bd.Issue, n), row: make([]int, n), offset: make([]int, n), col: 2}
}

func (v *kanbanView) Name() string { return "Board" }

func (v *kanbanView) inScope(a *App, is *bd.Issue) bool {
	if is.IssueType == "epic" || !a.machineMatch(is) {
		return false
	}
	if a.state.BoardEpic == "" {
		return true
	}
	for p := a.snap.ByID[is.Parent]; p != nil; p = a.snap.ByID[p.Parent] {
		if p.ID == a.state.BoardEpic {
			return true
		}
	}
	return false
}

func (v *kanbanView) Rebuild(a *App) {
	keep := ""
	if is := v.Selected(); is != nil {
		keep = is.ID
	}
	for c := range v.cards {
		v.cards[c] = v.cards[c][:0]
	}
	for _, is := range a.snap.Issues {
		if !v.inScope(a, is) {
			continue
		}
		for c, col := range kanbanColumns {
			if col.has(a.snap, is) {
				v.cards[c] = append(v.cards[c], is)
				break
			}
		}
	}
	for c := range v.cards {
		cs := v.cards[c]
		sort.SliceStable(cs, func(i, j int) bool {
			if cs[i].Priority != cs[j].Priority {
				return cs[i].Priority < cs[j].Priority
			}
			return cs[i].UpdatedAt.After(cs[j].UpdatedAt)
		})
		v.row[c] = clamp(v.row[c], 0, max(len(cs)-1, 0))
	}
	// follow the selected card if it moved to another column
	for c, cs := range v.cards {
		for i, is := range cs {
			if is.ID == keep {
				v.col, v.row[c] = c, i
				return
			}
		}
	}
}

func (v *kanbanView) Selected() *bd.Issue {
	cs := v.cards[v.col]
	if len(cs) == 0 {
		return nil
	}
	return cs[clamp(v.row[v.col], 0, len(cs)-1)]
}

func (v *kanbanView) Update(a *App, k tea.KeyPressMsg) (bool, tea.Cmd) {
	switch k.String() {
	case "h", "left":
		v.col = max(v.col-1, 0)
	case "l", "right":
		v.col = min(v.col+1, len(kanbanColumns)-1)
	case "j", "down":
		v.row[v.col] = min(v.row[v.col]+1, max(len(v.cards[v.col])-1, 0))
	case "k", "up":
		v.row[v.col] = max(v.row[v.col]-1, 0)
	case "home":
		v.row[v.col] = 0
	case "end", "G":
		v.row[v.col] = max(len(v.cards[v.col])-1, 0)
	case "H", "<":
		return true, v.move(a, -1)
	case "L", ">":
		return true, v.move(a, 1)
	case "E":
		a.modal = v.scopePicker(a)
	default:
		return false, nil
	}
	return true, nil
}

// move sends the selected (or marked) cards to the neighbouring column by setting its status.
func (v *kanbanView) move(a *App, dir int) tea.Cmd {
	to := v.col + dir
	if to < 0 || to >= len(kanbanColumns) || v.Selected() == nil {
		return nil
	}
	ts := a.targets()
	col := kanbanColumns[to]
	if col.status == "closed" {
		a.closeFlow(ts)
		return nil
	}
	return a.update(ts, a.label(ts)+" → "+col.name, func(i *bd.Issue) { i.Status = col.status }, "--status", col.status)
}

func (v *kanbanView) scopePicker(a *App) *modal {
	opts := []option{{"", "all epics"}}
	for _, is := range a.snap.Issues {
		if is.IssueType == "epic" && !is.Closed() {
			opts = append(opts, option{is.ID, a.shortID(is.ID) + "  " + is.Title})
		}
	}
	return newPicker("Board scope", opts, func(id string) tea.Cmd {
		a.state.BoardEpic = id
		v.Rebuild(a)
		return saveState(a)
	})
}

func (v *kanbanView) Hints() []string {
	return []string{"h/l column", "H/L move card", "E epic scope"}
}

const minColW = 24

func (v *kanbanView) Render(a *App, w, h int) string {
	scope := "all epics"
	if e := a.snap.ByID[a.state.BoardEpic]; e != nil {
		scope = a.shortID(e.ID) + " " + e.Title
	}
	bar := ansi.Truncate(fmt.Sprintf(" %s %s   %s %s", sDim.Render("scope"), scope, sDim.Render("machine"), machineHint(a)), w, "…")
	h -= 2

	// show as many columns as fit, keeping the focused one in the window
	n := clamp(w/minColW, 1, len(kanbanColumns))
	first := clamp(v.col-n/2, 0, len(kanbanColumns)-n)
	cw := (w - (n - 1)) / n
	cols := make([][]string, n)
	for i := range n {
		cols[i] = v.renderColumn(a, first+i, cw, h)
	}
	var b strings.Builder
	for line := range h {
		for i := range n {
			if i > 0 {
				sep := "│"
				if line == 1 {
					sep = "┼"
				}
				b.WriteString(sRule.Render(sep))
			}
			cell := ""
			if line < len(cols[i]) {
				cell = cols[i][line]
			}
			b.WriteString(padRight(cell, cw))
		}
		if line < h-1 {
			b.WriteByte('\n')
		}
	}
	more := ""
	if first > 0 {
		more += "◂ "
	}
	if first+n < len(kanbanColumns) {
		more += "▸"
	}
	return bar + strings.Repeat(" ", max(w-ansi.StringWidth(bar)-ansi.StringWidth(more), 1)) + sDim.Render(more) + "\n\n" + b.String()
}

func (v *kanbanView) renderColumn(a *App, c, w, h int) []string {
	col, cs, focused := kanbanColumns[c], v.cards[c], c == v.col
	title := fmt.Sprintf(" %s %d", col.name, len(cs))
	if focused {
		title = sTabOn.Render(title)
	} else {
		title = sBold.Render(title)
	}
	lines := []string{title, rule(w)}
	body := h - len(lines)
	if len(cs) == 0 {
		return append(lines, sDim.Render(" —"))
	}
	// cards are variable height, so render each and scroll by whole cards
	cards := make([][]string, len(cs))
	for i, is := range cs {
		cards[i] = v.card(a, is, w, focused && i == v.row[c])
	}
	sel := v.row[c]
	if sel < v.offset[c] {
		v.offset[c] = sel
	}
	for {
		used := 0
		for i := v.offset[c]; i <= sel; i++ {
			used += len(cards[i])
		}
		if used <= body || v.offset[c] >= sel {
			break
		}
		v.offset[c]++
	}
	if v.offset[c] > 0 {
		lines = append(lines, sDim.Render(fmt.Sprintf(" ↑ %d more", v.offset[c])))
	}
	for i := v.offset[c]; i < len(cards); i++ {
		if len(lines)+len(cards[i]) > h {
			lines = append(lines, sDim.Render(fmt.Sprintf(" ↓ %d more", len(cards)-i)))
			break
		}
		lines = append(lines, cards[i]...)
	}
	return lines[:min(len(lines), h)]
}

// card is two to three lines: status, id and priority, then the title wrapped to two lines.
func (v *kanbanView) card(a *App, is *bd.Issue, w int, sel bool) []string {
	edge := " "
	switch {
	case sel:
		edge = sAccent.Render("┃")
	case a.marks[is.ID]:
		edge = sOK.Render("+")
	case a.recentlyChanged(is.ID):
		edge = sAccent.Render("•")
	}
	head := fmt.Sprintf("%s %s %s", glyph(a.snap, is), sDim.Render(a.shortID(is.ID)), prio(is.Priority))
	if a.state.BoardEpic == "" {
		if e := a.snap.Epic(is); e != nil {
			head += sDim.Render(" · " + a.shortID(e.ID))
		}
	}
	if chain := a.snap.BlockChain(is); len(chain) > 0 && !is.Closed() {
		head += sErr.Render(" ⊘ " + a.shortID(chain[len(chain)-1].ID))
	}
	if ag := a.agentNote(is); ag != "" && is.Status == "in_progress" {
		head += " " + ag
	}
	inner := w - 3
	out := []string{edge + " " + ansi.Truncate(head, inner, "…")}
	title := is.Title
	if sel {
		title = sSel.Render(title)
	}
	wrapped := strings.Split(ansi.Wordwrap(title, inner, " "), "\n")
	if len(wrapped) > 2 {
		wrapped = append(wrapped[:1], ansi.Truncate(strings.Join(wrapped[1:], " "), inner, "…"))
	}
	for _, l := range wrapped {
		out = append(out, edge+" "+ansi.Truncate(l, inner, "…"))
	}
	return append(out, "")
}
