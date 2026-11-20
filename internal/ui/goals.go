package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/FoamScience/beads-tui/internal/goals"
	"github.com/charmbracelet/x/ansi"
)

type (
	goalsEditedMsg struct{ err error }
	goalBeadMsg    struct {
		key, id string
		err     error
	}
)

// loadGoals rereads goals.toml and its sidecar; the tags in Now and Backlog follow the current cycle.
func (a *App) loadGoals() {
	s, err := goals.Load(goals.Path())
	a.goals, a.goalsErr = s, err
	a.goalTags = s.Tags(a.goalCycle(s.Current(time.Now())))
}

func (a *App) goalCycle(i int) *goals.Cycle {
	if a.goals == nil || a.goals.File == nil || i < 0 || i >= len(a.goals.File.Cycles) {
		return nil
	}
	return a.goals.File.Cycles[i]
}

// goalTag lists the goals an issue works toward: those its own bead or any ancestor is an item of.
func (a *App) goalTag(is *bd.Issue) string {
	var out []string
	for p := is; p != nil; p = a.snap.ByID[p.Parent] {
		for _, g := range a.goalTags[p.ID] {
			if !slices.Contains(out, g) {
				out = append(out, g)
			}
		}
	}
	slices.Sort(out)
	return strings.Join(out, ",")
}

type goalsView struct {
	cycle      int
	pane       int // 0 goals, 1 items, 2 next
	goal, item int
	sel        *bd.Issue // the selected item's bead
	noBead     bool      // the selected item has no bead yet, so n creates one
	next       issueList
}

func newGoals() *goalsView { return &goalsView{cycle: -1} }

func (v *goalsView) Name() string { return "Goals" }

// Activate rereads goals.toml; a cycle picked with c stays picked while it still exists.
func (v *goalsView) Activate(a *App) tea.Cmd {
	a.loadGoals()
	if a.goalCycle(v.cycle) == nil {
		v.cycle = a.goals.Current(time.Now())
	}
	v.Rebuild(a)
	return nil
}

func (v *goalsView) cur(a *App) (*goals.Cycle, *goals.Goal, *goals.Item) {
	c := a.goalCycle(v.cycle)
	if c == nil || len(c.Goals) == 0 {
		return c, nil, nil
	}
	v.goal = clamp(v.goal, 0, len(c.Goals)-1)
	g := c.Goals[v.goal]
	if len(g.Items) == 0 {
		return c, g, nil
	}
	v.item = clamp(v.item, 0, len(g.Items)-1)
	return c, g, g.Items[v.item]
}

func (v *goalsView) itemIssue(a *App) *bd.Issue {
	c, g, it := v.cur(a)
	if it == nil {
		return nil
	}
	return a.snap.ByID[a.goals.Bead(c, g, it)]
}

// Rebuild lists what can move the selected item: its bead and descendants in progress or ready, under the machine filter.
func (v *goalsView) Rebuild(a *App) {
	if v.cycle < 0 && a.goals != nil {
		v.cycle = a.goals.Current(time.Now())
	}
	root := v.itemIssue(a)
	v.sel = root
	c, g, it := v.cur(a)
	v.noBead = it != nil && a.goals.State(a.snap, c, g, v.item) == goals.NoBead
	var list []*bd.Issue
	var walk func(is *bd.Issue)
	walk = func(is *bd.Issue) {
		if (is.Status == "in_progress" || a.snap.Ready(is)) && a.machineMatch(is) {
			list = append(list, is)
		}
		for _, c := range a.snap.Children[is.ID] {
			walk(c)
		}
	}
	if root != nil {
		walk(root)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if pi, pj := list[i].Status == "in_progress", list[j].Status == "in_progress"; pi != pj {
			return pi
		}
		if list[i].Priority != list[j].Priority {
			return list[i].Priority < list[j].Priority
		}
		return bd.IDLess(list[i].ID, list[j].ID)
	})
	rows := make([]row, len(list))
	for i, is := range list {
		rows[i] = row{issue: is}
	}
	v.next.SetRows(rows)
}

func (v *goalsView) Selected() *bd.Issue {
	switch v.pane {
	case 1:
		return v.sel
	case 2:
		return v.next.Selected()
	}
	return nil
}

func (v *goalsView) Update(a *App, k tea.KeyPressMsg) (bool, tea.Cmd) {
	c, g, it := v.cur(a)
	key := k.String()
	switch key {
	case "h", "left":
		v.pane = max(v.pane-1, 0)
		return true, nil
	case "l", "right":
		v.pane = min(v.pane+1, 2)
		return true, nil
	case "E":
		path := goals.Path()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return true, flashErr(err)
		}
		return true, tea.ExecProcess(editorCmd(path), func(err error) tea.Msg { return goalsEditedMsg{err} })
	}
	switch v.pane {
	case 0:
		switch key {
		case "j", "down", "k", "up":
			if c != nil && len(c.Goals) > 0 {
				d := 1
				if key == "k" || key == "up" {
					d = -1
				}
				v.goal, v.item = clamp(v.goal+d, 0, len(c.Goals)-1), 0
				v.Rebuild(a)
			}
			return true, nil
		case "enter":
			v.pane = 1
			return true, nil
		case "c":
			if a.goals.File != nil && len(a.goals.File.Cycles) > 0 {
				v.cycle, v.goal, v.item = (v.cycle+1)%len(a.goals.File.Cycles), 0, 0
				v.Rebuild(a)
			}
			return true, nil
		}
	case 1:
		switch key {
		case "j", "down", "k", "up":
			if g != nil && len(g.Items) > 0 {
				d := 1
				if key == "k" || key == "up" {
					d = -1
				}
				v.item = clamp(v.item+d, 0, len(g.Items)-1)
				v.Rebuild(a)
			}
			return true, nil
		case "x":
			if it == nil {
				return true, nil
			}
			return true, v.accept(a, c, g, it)
		case "n":
			if it != nil && a.goals.State(a.snap, c, g, v.item) == goals.NoBead {
				return true, v.createBead(a, c, g, it)
			}
		}
	case 2:
		if v.next.HandleKey(key) {
			return true, nil
		}
	}
	return false, nil
}

func (v *goalsView) accept(a *App, c *goals.Cycle, g *goals.Goal, it *goals.Item) tea.Cmd {
	if c.Past(time.Now()) {
		return flash(c.ID + " has ended; its scores are frozen")
	}
	key := goals.Key(c, g, it)
	prev, had := a.goals.Side.Accepted[key]
	switch a.goals.State(a.snap, c, g, v.item) {
	case goals.Accepted:
		delete(a.goals.Side.Accepted, key)
	case goals.Locked:
		return flash("accept the rungs before " + it.Label + " first")
	case goals.NoBead:
		return flash(it.Label + " has no bead yet: n creates one")
	case goals.Missing:
		return flash(it.Label + ": bead " + a.goals.Bead(c, g, it) + " is not in the DB")
	default:
		a.goals.Side.Accepted[key] = time.Now().Format(time.DateOnly)
	}
	if err := a.goals.SaveSidecar(); err != nil {
		if had {
			a.goals.Side.Accepted[key] = prev
		} else {
			delete(a.goals.Side.Accepted, key)
		}
		return flashErr(err)
	}
	v.Rebuild(a)
	return nil
}

func (v *goalsView) createBead(a *App, c *goals.Cycle, g *goals.Goal, it *goals.Item) tea.Cmd {
	key, cl := goals.Key(c, g, it), a.client
	args := []string{"create", "-t", "task", "--silent", "-l", "machine:" + a.host}
	if g.KR != "" {
		args = append(args, "-l", "kr:"+g.KR)
	}
	args = append(args, "--", it.Label)
	a.pending++
	a.flash, a.flashErr, a.flashAt = "⟳ create bead for "+it.Label, false, time.Now()
	return func() tea.Msg {
		out, err := cl.Output(args...)
		return goalBeadMsg{key, strings.TrimSpace(string(out)), err}
	}
}

func (v *goalsView) Msg(a *App, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case goalsEditedMsg:
		a.loadGoals()
		v.Rebuild(a)
		if m.err != nil {
			return flashErr(m.err)
		}
		return nil
	case goalBeadMsg:
		if m.err == nil && m.id == "" {
			m.err = fmt.Errorf("bd create printed no id")
		}
		if m.err == nil {
			a.goals.Side.Beads[m.key] = m.id
			if m.err = a.goals.SaveSidecar(); m.err != nil {
				delete(a.goals.Side.Beads, m.key)
				m.err = fmt.Errorf("created %s but could not record it: %w", m.id, m.err)
			}
			a.goalTags = a.goals.Tags(a.goalCycle(a.goals.Current(time.Now())))
		}
		return func() tea.Msg { return writeDoneMsg{"bead " + m.id + " for " + m.key, m.err} }
	}
	return nil
}

func (v *goalsView) Hints() []string {
	switch v.pane {
	case 0:
		return []string{"h/l pane", "c cycle", "E edit goals", "M machine"}
	case 1:
		if v.noBead {
			return []string{"h/l pane", "n create bead", "E edit goals"}
		}
		return []string{"h/l pane", "x accept", "E edit goals"}
	}
	return []string{"h/l pane", "C claim", "M machine"}
}

// goalBar draws earned, then closed-but-unconfirmed as a ghost, then the rest.
func goalBar(earned, done float64, w int) string {
	e := int(earned * float64(w) / 100)
	d := min(int((earned+done)*float64(w)/100), w) - e
	return sAccent.Render(strings.Repeat("▰", e)) + sWarn.Faint(true).Render(strings.Repeat("▰", d)) + sDim.Render(strings.Repeat("▱", w-e-d))
}

var goalGlyph = map[goals.State]string{
	goals.NoBead:     sWarn.Render("?"),
	goals.Missing:    sErr.Render("?"),
	goals.Open:       "·",
	goals.InProgress: sAccent.Render("●"),
	goals.Done:       sWarn.Render("◌"),
	goals.Accepted:   sOK.Render("✓"),
	goals.Locked:     sDim.Render("⊘"),
}

func (v *goalsView) Render(a *App, w, h int) string {
	if a.goalsErr != nil {
		return sErr.Render("\n  "+a.goalsErr.Error()) + sDim.Render("\n\n  E edits "+goals.Path())
	}
	c := a.goalCycle(v.cycle)
	if c == nil {
		return sDim.Render("\n  no goals yet: E creates " + goals.Path() + "\n\n" + goalsExample)
	}
	now := time.Now()
	tp := c.TimePct(now)
	score := a.goals.CycleScore(a.snap, c)
	elapsed, days := c.Days(now)
	head := fmt.Sprintf(" %s   %s %s %s   %s %s %s   %s %s %s",
		machineChip(a), sBold.Render(c.ID), sDim.Render(c.Start.Format("02 Jan")+" → "+c.End.Format("02 Jan 2006")), sDim.Render(fmt.Sprintf("day %d/%d", elapsed, days)),
		sDim.Render("time"), progressBar(int(tp), 100, 10), sDim.Render(fmt.Sprintf("%.0f%%", tp)),
		sDim.Render("earned"), goalBar(score, 0, 10), fmt.Sprintf("%.0f%%", score))
	if c.Past(now) {
		head += "  " + sDim.Render("ended")
	}
	out := ansi.Truncate(head, w, "…")
	if len(a.goals.Warning) > 0 {
		out += "\n " + sWarn.Render(ansi.Truncate("⚠ "+strings.Join(a.goals.Warning, "; "), w-2, "…"))
	} else {
		out += "\n"
	}
	h -= 2
	lw := clamp(w*2/5, 30, 48)
	rw := w - lw - 3
	left := v.renderGoals(a, c, lw, h, tp)
	right := v.renderItems(a, c, rw, h)
	ls, rs := strings.Split(left, "\n"), strings.Split(right, "\n")
	var b strings.Builder
	b.WriteString(out)
	for i := range h {
		l, r := "", ""
		if i < len(ls) {
			l = ls[i]
		}
		if i < len(rs) {
			r = rs[i]
		}
		b.WriteString("\n" + padRight(ansi.Truncate(l, lw, "…"), lw) + " " + sRule.Render("│") + " " + r)
	}
	return b.String()
}

func (v *goalsView) renderGoals(a *App, c *goals.Cycle, w, h int, tp float64) string {
	if len(c.Goals) == 0 {
		return sDim.Render("  no goals in " + c.ID)
	}
	lines := []string{v.title("GOALS", 0)}
	barW := max(w-28, 6) // room for the label, the percentage and a "⚠ behind" flag
	for i, g := range c.Goals {
		marker := "  "
		if i == v.goal {
			marker = sAccent.Render("▸ ")
			if v.pane != 0 {
				marker = sDim.Render("▸ ")
			}
		}
		tag := g.Type
		if g.KR != "" {
			tag = "kr" + g.KR + " " + tag
		}
		title := g.ID + " " + g.Title
		if i == v.goal {
			title = sSel.Render(title)
		}
		lines = append(lines, marker+ansi.Truncate(title, max(w-lipgloss.Width(tag)-4, 8), "…")+" "+sDim.Render(tag))
		earned, done := a.goals.Score(a.snap, c, g)
		e := fmt.Sprintf("    %s %s %3.0f%%", sDim.Render("earned"), goalBar(earned, done, barW), earned)
		if done > 0 {
			e += sWarn.Faint(true).Render(fmt.Sprintf(" ◌+%.0f", done))
		}
		t := fmt.Sprintf("    %s   %s %3.0f%%", sDim.Render("time"), progressBar(int(tp), 100, barW), tp)
		if !c.Past(time.Now()) && goals.Behind(tp, earned) {
			t += sErr.Render(fmt.Sprintf(" ⚠ behind %.0f", tp-earned))
		}
		lines = append(lines, e, t, "")
	}
	return strings.Join(lines[:min(len(lines), h)], "\n")
}

func (v *goalsView) title(s string, pane int) string {
	if v.pane == pane {
		return " " + sSection.Render(s)
	}
	return " " + sDim.Render(s)
}

func (v *goalsView) renderItems(a *App, c *goals.Cycle, w, h int) string {
	_, g, it := v.cur(a)
	if g == nil {
		return ""
	}
	earned, done := a.goals.Score(a.snap, c, g)
	lines := []string{
		v.title(ansi.Truncate(g.ID+" "+g.Title, w-2, "…"), 1),
		" " + sDim.Render(fmt.Sprintf("%s · earned %.0f%% · done +%.0f%%", g.Scoring, earned, done)),
		"",
	}
	labelW := max(w-a.idWidth-30, 10)
	for i, x := range g.Items {
		st := a.goals.State(a.snap, c, g, i)
		marker := "  "
		if i == v.item {
			marker = sDim.Render("▸ ")
			if v.pane == 1 {
				marker = sAccent.Render("▸ ")
			}
		}
		id := a.goals.Bead(c, g, x)
		label := padRight(ansi.Truncate(x.Label, labelW, "…"), labelW)
		if i == v.item {
			label = sSel.Render(label)
		}
		lines = append(lines, fmt.Sprintf("%s%s %s %s %s %s", marker, goalGlyph[st], label,
			padLeft(sDim.Render(fmt.Sprintf("%.4g", x.Pts)), 5), padRight(sDim.Render(a.shortID(id)), a.idWidth), v.stateText(a, c, g, x, st)))
	}
	itemsH := min(len(lines), max(h/2, 6))
	if len(lines) > itemsH && v.item+4 >= itemsH {
		off := min(v.item+4-itemsH+1, len(lines)-itemsH)
		lines = append(lines[:3], lines[3+off:]...)
	}
	lines = lines[:min(len(lines), itemsH)]
	nextTitle := "NEXT"
	if it != nil {
		nextTitle += " under " + it.Label
	}
	lines = append(lines, rule(w), v.title(ansi.Truncate(nextTitle, w-2, "…"), 2))
	if len(v.next.rows) == 0 {
		lines = append(lines, sDim.Render("  nothing ready or in progress here"))
	} else {
		lines = append(lines, v.next.Render(a, w, max(h-len(lines), 1)))
	}
	return strings.Join(lines, "\n")
}

func (v *goalsView) stateText(a *App, c *goals.Cycle, g *goals.Goal, it *goals.Item, st goals.State) string {
	switch st {
	case goals.NoBead:
		return sWarn.Render("no bead · n creates")
	case goals.Missing:
		return sErr.Render("not in DB")
	case goals.Open:
		return sDim.Render("open")
	case goals.InProgress:
		return sAccent.Render("in progress")
	case goals.Done:
		return sWarn.Render("done · x accepts")
	case goals.Accepted:
		d, _ := a.goals.AcceptedOn(c, g, it)
		return sOK.Render("accepted " + d)
	}
	return sDim.Render("locked")
}

const goalsExample = `  [[cycle]]
  id    = "T1-2027"
  start = 2026-12-01
  end   = 2027-02-28

    [[cycle.goal]]
    id      = "G1"
    title   = "Viewer live for both products"
    kr      = "1.1"
    type    = "performance"   # performance | ai | competence
    scoring = "points"        # points | ladder
    items = [
      { label = "SR-141 foundation", bead = "tasks-prd.23", pts = 8 },
      { label = "internal test order", pts = 25 },   # n creates its bead
    ]`
