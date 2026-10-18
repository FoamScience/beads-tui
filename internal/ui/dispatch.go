package ui

import (
	"cmp"
	"fmt"
	"image/color"
	"os/exec"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/FoamScience/beads-tui/internal/dispatch"
	"github.com/charmbracelet/x/ansi"
)

const maxEvents = 200

// Dispatch is the bt dispatch screen: it runs the engine every tick and edits dispatch.toml.
type Dispatch struct {
	eng     *dispatch.Engine
	rep     dispatch.Report
	events  []string
	w, h    int
	inQueue bool // focus on the selected lane's queue instead of the lanes table
	row     int  // index into rows()
	bead    int
	msg     string
	at      time.Time
	modal   *modal
	state   *state
	graph   *graphView
	graphOf string         // the bead the graph was built for
	graphOn bool           // the graph is on screen instead of the lanes
	laneAt  map[int]string // screen row -> lane drawn there, from the last render
	click   tea.Mouse      // last left click, to spot a double click
	clickAt time.Time
}

// laneRow is a line of the lanes table: a group header when lane is "", else one lane.
type laneRow struct{ group, lane string }

type stepMsg dispatch.Report

func NewDispatch(e *dispatch.Engine) *Dispatch {
	return &Dispatch{eng: e, state: loadState(), graph: &graphView{showClosed: true}}
}

func (d *Dispatch) step() tea.Cmd {
	return func() tea.Msg { return stepMsg(d.eng.Step()) }
}

func (d *Dispatch) Init() tea.Cmd { return d.step() }

func (d *Dispatch) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		d.w, d.h = msg.Width, msg.Height
	case stepMsg:
		// an errored tick keeps the last good picture on screen; the error goes to the event log
		r := dispatch.Report(msg)
		if r.Err != nil {
			d.events = append(d.events, time.Now().Format("15:04:05 ")+"error: "+r.Err.Error())
		} else {
			d.rep, d.at = r, time.Now()
		}
		d.events = append(d.events, r.Events...)
		if n := len(d.events) - maxEvents; n > 0 {
			d.events = d.events[n:]
		}
		return d, tea.Tick(dispatch.Tick, func(time.Time) tea.Msg { return stepMsg(d.eng.Step()) })
	case editDoneMsg:
		if msg.err != nil {
			d.msg = "editor: " + msg.err.Error()
		}
	case flashMsg:
		d.msg = msg.text
	case tea.MouseClickMsg:
		return d, d.mouseClick(msg.Mouse())
	case tea.KeyPressMsg:
		d.msg = ""
		if d.modal != nil {
			done, cmd := d.modal.update(msg)
			if done {
				d.modal = nil
			}
			return d, cmd
		}
		if d.graphOn {
			d.graphKey(msg)
			return d, nil
		}
		return d, d.key(msg.String())
	}
	return d, nil
}

func (d *Dispatch) lanes() []string {
	if d.rep.Cfg == nil {
		return nil
	}
	return d.rep.Cfg.Names()
}

// groups maps each group to its lanes, both sorted.
func (d *Dispatch) groups() ([]string, map[string][]string) {
	byGroup := map[string][]string{}
	for _, name := range d.lanes() {
		g := d.rep.Cfg.Lanes[name].GroupName()
		byGroup[g] = append(byGroup[g], name)
	}
	names := make([]string, 0, len(byGroup))
	for g := range byGroup {
		names = append(names, g)
	}
	slices.Sort(names)
	return names, byGroup
}

func (d *Dispatch) rows() []laneRow {
	names, byGroup := d.groups()
	var rows []laneRow
	for _, g := range names {
		rows = append(rows, laneRow{group: g})
		if !slices.Contains(d.state.Collapsed, g) {
			for _, l := range byGroup[g] {
				rows = append(rows, laneRow{g, l})
			}
		}
	}
	return rows
}

func (d *Dispatch) selRow() (laneRow, bool) {
	rows := d.rows()
	if len(rows) == 0 {
		return laneRow{}, false
	}
	d.row = clamp(d.row, 0, len(rows)-1)
	return rows[d.row], true
}

// selLane is the lane under the cursor, nil on a group header.
func (d *Dispatch) selLane() *dispatch.LaneView {
	if r, ok := d.selRow(); ok && r.lane != "" {
		return d.rep.Lanes[r.lane]
	}
	return nil
}

func (d *Dispatch) selBead() *bd.Issue {
	v := d.selLane()
	if v == nil || len(v.Queue) == 0 {
		return nil
	}
	d.bead = clamp(d.bead, 0, len(v.Queue)-1)
	return v.Queue[d.bead]
}

func (d *Dispatch) key(k string) tea.Cmd {
	switch k {
	case "q", "ctrl+c":
		running := d.running()
		if len(running) == 0 {
			return tea.Quit
		}
		d.modal = newConfirm(fmt.Sprintf("%d lanes still working:\n  %s\nQuitting stops dispatching; their sessions keep running. Quit?",
			len(running), strings.Join(running, "\n  ")), func() tea.Cmd { return tea.Quit })
	case "tab", "h", "l", "left", "right":
		d.inQueue = !d.inQueue
	case "j", "down":
		d.move(1)
	case "k", "up":
		d.move(-1)
	case "enter":
		if r, ok := d.selRow(); ok && r.lane == "" && !d.inQueue {
			d.state.Collapsed = dispatch.Toggle(d.state.Collapsed, r.group)
			if err := d.state.save(); err != nil {
				d.msg = "state: " + err.Error()
			}
		}
	case "space":
		v := d.selLane()
		if r, ok := d.selRow(); ok && r.lane == "" && !d.inQueue {
			d.allowGroup(r.group)
			return nil
		}
		switch {
		case v == nil:
		case !d.inQueue:
			d.edit(func(a *dispatch.Allow) { a.Lanes = dispatch.Toggle(a.Lanes, v.Lane.Name) }, "lane "+v.Lane.Name)
		case d.selBead() != nil:
			id := d.selBead().ID
			d.edit(func(a *dispatch.Allow) { a.Beads = dispatch.Toggle(a.Beads, id) }, "bead "+id)
		}
	case "e":
		if is := d.selBead(); d.inQueue && is != nil && d.rep.Snap != nil {
			if ep := d.rep.Snap.Epic(is); ep != nil {
				d.edit(func(a *dispatch.Allow) { a.Epics = dispatch.Toggle(a.Epics, ep.ID) }, "epic "+ep.ID)
			} else {
				d.msg = is.ID + " has no epic"
			}
		}
	case "D":
		if v := d.selLane(); v != nil && !d.inQueue {
			d.deleteLane(v)
		}
	case "g":
		if d.graphBead() == nil {
			d.msg = "no running bead to graph here"
		} else {
			d.graphOn = true
		}
	case "E":
		return tea.ExecProcess(editorCmd(dispatch.AllowPath()), func(err error) tea.Msg { return editDoneMsg{"dispatch.toml", err} })
	}
	return nil
}

// allowGroup allows every lane in the group, or disallows them all when all are already allowed.
func (d *Dispatch) allowGroup(g string) {
	_, byGroup := d.groups()
	lanes := byGroup[g]
	all := true
	for _, l := range lanes {
		all = all && slices.Contains(d.rep.Allow.Lanes, l)
	}
	d.edit(func(a *dispatch.Allow) {
		for _, l := range lanes {
			if has := slices.Contains(a.Lanes, l); has == all {
				a.Lanes = dispatch.Toggle(a.Lanes, l)
			}
		}
	}, "group "+g)
}

// deleteLane asks before removing a lane that has no open beads and nothing sent to it.
func (d *Dispatch) deleteLane(v *dispatch.LaneView) {
	name := v.Lane.Name
	open := v.Count[dispatch.Queued] + v.Count[dispatch.Waiting] + v.Count[dispatch.InProgress] + v.Count[dispatch.Partial]
	switch {
	case open > 0:
		d.msg = fmt.Sprintf("lane %s still has %d open beads; move or close them first", name, open)
		return
	case d.rep.Pending[name] != "":
		d.msg = "lane " + name + " was just sent " + d.rep.Pending[name]
		return
	}
	file := "lanes.toml"
	if v.Lane.Auto {
		file = "lanes.auto.toml"
	}
	q := fmt.Sprintf("Delete lane %s from %s?", name, file)
	if v.Live {
		q += "\nIts session in pane " + v.Pane.ID + " keeps running."
	}
	d.modal = newConfirm(q, func() tea.Cmd {
		if err := dispatch.DeleteLane(dispatch.ConfigPath(), name, v.Lane.Auto); err != nil {
			d.msg = "delete " + name + ": " + err.Error()
			return nil
		}
		if slices.Contains(d.rep.Allow.Lanes, name) {
			d.edit(func(a *dispatch.Allow) { a.Lanes = dispatch.Toggle(a.Lanes, name) }, "")
		}
		d.msg = "deleted lane " + name
		return nil
	})
}

// running lists lanes whose pane is busy or that were sent a bead not claimed yet; an idle pane
// holding an in_progress bead has stopped and is not running.
func (d *Dispatch) running() []string {
	var out []string
	for _, name := range d.lanes() {
		v := d.rep.Lanes[name]
		switch {
		case d.rep.Pending[name] != "":
			out = append(out, name+"  "+d.rep.Pending[name]+" (sent, not claimed yet)")
		case v.Live && (v.Pane.Status == "working" || v.Pane.Status == "blocked"):
			out = append(out, fmt.Sprintf("%s  %s (%s)", name, cmp.Or(v.Active, "-"), v.Pane.Status))
		}
	}
	return out
}

func (d *Dispatch) move(n int) {
	if d.inQueue {
		d.bead += n
		d.selBead()
		return
	}
	d.row += n
	d.bead = 0
	d.selRow()
}

// edit rewrites dispatch.toml from the file on disk, not the last tick's copy, so a hand edit is never lost.
func (d *Dispatch) edit(f func(*dispatch.Allow), what string) {
	a, err := dispatch.LoadAllow(dispatch.AllowPath())
	if err != nil {
		d.msg = "dispatch.toml: " + err.Error() + " (fix it with E)"
		return
	}
	f(&a)
	if err := dispatch.SaveAllow(dispatch.AllowPath(), a); err != nil {
		d.msg = "dispatch.toml: " + err.Error()
		return
	}
	d.rep.Allow, d.msg = a, "toggled "+what
}

// mouseClick focuses the herdr pane of the lane double-clicked in its LANE or PANE cell.
func (d *Dispatch) mouseClick(m tea.Mouse) tea.Cmd {
	if m.Button != tea.MouseLeft || d.modal != nil || d.graphOn {
		return nil
	}
	double := m.X == d.click.X && m.Y == d.click.Y && time.Since(d.clickAt) < doubleClick
	d.click, d.clickAt = m, time.Now()
	from := 2 + laneCols[0] + 1
	if !double || m.X < from || m.X >= from+laneCols[1]+1+laneCols[2] {
		return nil
	}
	v := d.rep.Lanes[d.laneAt[m.Y]]
	if v == nil {
		return nil
	}
	if !v.Live {
		d.msg = "lane " + v.Lane.Name + " has no live pane"
		return nil
	}
	p := v.Pane
	return func() tea.Msg {
		if err := exec.Command("herdr", "workspace", "focus", p.Workspace).Run(); err != nil {
			return flashMsg{text: "herdr: " + err.Error(), err: true}
		}
		_ = exec.Command("herdr", "tab", "focus", p.Tab).Run()
		return flashMsg{text: "focused " + v.Lane.Name + " / pane " + p.ID}
	}
}

const doubleClick = 400 * time.Millisecond

func (d *Dispatch) View() tea.View {
	v := tea.NewView(d.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "bt dispatch"
	return v
}

func (d *Dispatch) render() string {
	if d.w == 0 {
		return ""
	}
	mode := sOK.Render("● live")
	if d.eng.DryRun {
		mode = sWarn.Render("● dry run")
	}
	a := d.rep.Allow
	head := fmt.Sprintf("%s  %s  allowed: %d lanes · %d epics · %d beads", sTabOn.Render("bt dispatch"), mode, len(a.Lanes), len(a.Epics), len(a.Beads))
	if !d.at.IsZero() {
		head += sDim.Render("  · tick " + d.at.Format("15:04:05"))
	}
	out := []string{head, ""}
	switch {
	case d.graphOn:
		out = append(out, d.renderGraph(d.h-3)...)
	case d.rep.Cfg == nil:
		out = append(out, sWarn.Render("waiting for the first tick (or lanes.toml failed, see events)"), sRule.Render(strings.Repeat("─", d.w)))
	default:
		out = append(out, legend()...)
		out = append(out, "")
		out = append(out, d.renderLanes(len(out))...)
		out = append(out, sRule.Render(strings.Repeat("─", d.w)))
		ledgers := d.renderLedgers()
		// the queue takes half the rows left under it (less its title, rule, the ledgers and the footer), the event log the rest
		out = append(out, d.renderQueue(max(3, (d.h-len(out)-len(ledgers)-3)/2))...)
		out = append(out, sRule.Render(strings.Repeat("─", d.w)))
		out = append(out, ledgers...)
	}
	footer := d.hints()
	if d.graphOn {
		for len(out) < d.h-1 {
			out = append(out, "")
		}
	} else if room := d.h - len(out) - 1; room > 0 {
		ev := d.events[max(0, len(d.events)-room):]
		for _, e := range ev {
			out = append(out, sDim.Render(e))
		}
		for range room - len(ev) {
			out = append(out, "")
		}
	}
	out = append(out, footer)
	if d.modal != nil {
		box := lipgloss.Place(d.w, d.h, lipgloss.Center, lipgloss.Center, d.modal.view(d.w, d.h))
		return lipgloss.NewStyle().MaxWidth(d.w).MaxHeight(d.h).Render(box)
	}
	for i, l := range out {
		out[i] = ansi.Truncate(l, d.w, "…")
	}
	return strings.Join(out[:min(len(out), d.h)], "\n")
}

func graphPick(g *graphView, ok func(*bd.Issue) bool) string {
	for _, l := range g.layers {
		for _, n := range l {
			if !n.dummy && ok(n.is) {
				return n.id
			}
		}
	}
	return ""
}

// laneCols are the lanes table's fixed widths: state symbol, LANE PANE ACTIVE NEXT PROGRESS DONE ALLOWED.
var laneCols = []int{1, 19, 7, 17, 17, laneBarCols, 7, 12}

// laneCells lays out vals in laneCols from column start on; each is cut and padded to its width so nothing drifts.
func laneCells(start int, vals ...string) string {
	out := make([]string, len(vals))
	for i, v := range vals {
		c := start + i
		v = ansi.Truncate(v, laneCols[c], "…")
		if c == 6 {
			out[i] = padLeft(v, laneCols[c])
		} else {
			out[i] = padRight(v, laneCols[c])
		}
	}
	return strings.Join(out, " ")
}

// renderLanes draws the lanes table starting at screen row top, noting which lane sits on each row.
func (d *Dispatch) renderLanes(top int) []string {
	out := []string{sDim.Render("  " + laneCells(0, "", "LANE", "PANE", "ACTIVE", "NEXT", "PROGRESS", "DONE", "ALLOWED"))}
	d.laneAt = map[int]string{}
	_, byGroup := d.groups()
	for i, r := range d.rows() {
		cursor := " "
		if i == d.row {
			cursor = sAccent.Render("▶")
		}
		var line string
		if r.lane == "" {
			line = d.groupLine(cursor, r.group, byGroup[r.group])
		} else {
			line = d.laneLine(cursor, d.rep.Lanes[r.lane])
		}
		if i == d.row && !d.inQueue {
			line = sSel.Render(line)
		}
		if r.lane != "" {
			d.laneAt[top+len(out)] = r.lane
		}
		out = append(out, line)
		if r.lane != "" {
			out = append(out, "      "+laneFit(d.rep.Cfg.Lanes[r.lane]))
		}
	}
	return out
}

func legend() []string {
	return []string{
		"  " + paneStates[0].sym + sDim.Render(" working  ") + paneStates[1].sym + sDim.Render(" blocked  ") + paneStates[2].sym + sDim.Render(" idle  ") +
			paneStates[3].sym + sDim.Render(" stalled (idle, holding a claimed bead)  ") + paneStates[4].sym + sDim.Render(" no pane    ") + lanePrio(0).Render(" P0 ") + " " + lanePrio(1).Render(" P1 ") + " " + lanePrio(2).Render(" P2 ") + " " + lanePrio(3).Render(" P3 ") + " " + lanePrio(4).Render(" P4+ "),
	}
}

// scope is what a bead advances: its own children, else its parent's unless that is a top-level project epic, else just itself.
func scope(s *bd.Snapshot, is *bd.Issue) *bd.Issue {
	if len(s.Children[is.ID]) == 0 {
		if p := s.ByID[is.Parent]; p != nil && p.Parent != "" {
			return p
		}
	}
	return is
}

func (d *Dispatch) activeScope(v *dispatch.LaneView) (id string, done, total int) {
	is := d.rep.Snap.ByID[v.Active]
	if is == nil {
		return "", 0, 0
	}
	sc := scope(d.rep.Snap, is)
	if len(d.rep.Snap.Children[sc.ID]) == 0 {
		return sc.ID, 0, 1
	}
	done, total = d.rep.Snap.Progress(sc.ID)
	return sc.ID, done, total
}

// graphBead is what the graph shows: the selected queue bead, else the selected lane's active bead.
func (d *Dispatch) graphBead() *bd.Issue {
	if d.inQueue {
		return d.selBead()
	}
	if v := d.selLane(); v != nil {
		return d.rep.Snap.ByID[v.Active]
	}
	return nil
}

// graphApp is the slice of App the Graph view reads: the snapshot and the lane sessions for its badges.
func (d *Dispatch) graphApp() *App {
	a := &App{snap: d.rep.Snap, herdr: map[string]agentPane{}}
	for _, v := range d.rep.Lanes {
		if v.Live && v.Pane.Session != "" {
			a.herdr[v.Pane.Session] = agentPane{Pane: v.Pane.ID, Status: v.Pane.Status, Title: v.Lane.Name}
		}
	}
	return a
}

// graphKey drives the full-screen graph: hjkl moves, H toggles closed beads, g/esc/q goes back.
func (d *Dispatch) graphKey(k tea.KeyPressMsg) {
	switch k.String() {
	case "g", "esc", "q":
		d.graphOn = false
	case "h", "j", "k", "l", "left", "down", "up", "right", "H":
		if d.graphBead() != nil {
			d.graph.Update(d.graphApp(), k)
		}
	}
}

// renderGraph draws the bead's scope with bt's Graph view, the bead selected.
func (d *Dispatch) renderGraph(h int) []string {
	is := d.graphBead()
	if is == nil {
		return []string{sDim.Render("no running bead on this row to graph")}
	}
	g, a := d.graph, d.graphApp()
	root := scope(d.rep.Snap, is).ID
	if g.fixed != d.rep.Snap || g.rootID != root || d.graphOf != is.ID {
		if g.rootID != root {
			g.offX, g.offY = 0, 0
		}
		g.fixed, g.rootID, g.sel, d.graphOf = d.rep.Snap, root, is.ID, is.ID
		g.Rebuild(a)
		if g.nodes[is.ID] == nil { // the bead is the graphed parent: select the child being worked, else the first open one
			if id := graphPick(g, func(c *bd.Issue) bool { return c.Status == "in_progress" }); id != "" {
				g.sel = id
			} else if id := graphPick(g, func(c *bd.Issue) bool { return !c.Closed() }); id != "" {
				g.sel = id
			}
		}
	}
	return strings.Split(g.Render(a, d.w, h), "\n")
}

func scopeCells(done, total int) (bar, frac string) {
	if total == 0 {
		return sDim.Render(strings.Repeat("▱", laneBarCols)), "-"
	}
	return progressBar(done, total, laneBarCols), fmt.Sprintf("%d/%d", done, total)
}

// laneFit describes the work a lane suits, from what the decider and the routing rules go by.
func laneFit(l *dispatch.Lane) string {
	var parts []string
	if l.About != "" {
		parts = append(parts, l.About)
	}
	if len(l.Takes.Labels) > 0 {
		parts = append(parts, "takes labels "+strings.Join(l.Takes.Labels, ", "))
	}
	if len(l.Takes.Epics) > 0 {
		parts = append(parts, "takes epics "+strings.Join(l.Takes.Epics, ", "))
	}
	if len(parts) == 0 {
		return sWarn.Render("no about in lanes.toml: the decider cannot route work here, only lane: labels can")
	}
	return sDim.Render(strings.Join(parts, " · "))
}

func (d *Dispatch) laneLine(cursor string, v *dispatch.LaneView) string {
	name := v.Lane.Name
	pane, status := "-", paneStates[4].sym
	switch {
	case !v.Live:
	case v.Pane.Status == "idle" && v.Active != "":
		pane, status = v.Pane.ID, paneStates[3].sym
	default:
		pane, status = v.Pane.ID, sDim.Render("?")
		for _, st := range paneStates[:3] {
			if st.name == v.Pane.Status {
				status = st.sym
			}
		}
	}
	next, allowed := "-", 0
	for _, is := range v.Queue {
		if d.rep.Allow.Permits(d.rep.Snap, is, name) {
			if allowed == 0 {
				next = is.ID
			}
			allowed++
		}
	}
	allow := fmt.Sprintf("%d of %d", allowed, len(v.Queue))
	if slices.Contains(d.rep.Allow.Lanes, name) {
		allow = sOK.Render("whole lane")
	}
	if v.Top >= 0 {
		name = lanePrio(v.Top).Render(" " + name + " ")
	} else {
		name = " " + name + " "
	}
	if v.Lane.Auto {
		name += sDim.Render(" auto")
	}
	_, done, total := d.activeScope(v)
	bar, frac := scopeCells(done, total)
	return cursor + " " + laneCells(0, status, " "+name, pane, cmp.Or(v.Active, "-"), next, bar, frac, allow)
}

// groupLine sums the progress its lanes' active beads advance, each scope once; its title spans the LANE to NEXT columns.
func (d *Dispatch) groupLine(cursor, g string, lanes []string) string {
	done, total, allowed := 0, 0, 0
	seen := map[string]bool{}
	for _, l := range lanes {
		if id, dn, tt := d.activeScope(d.rep.Lanes[l]); id != "" && !seen[id] {
			seen[id] = true
			done, total = done+dn, total+tt
		}
		if slices.Contains(d.rep.Allow.Lanes, l) {
			allowed++
		}
	}
	fold := "▾"
	if slices.Contains(d.state.Collapsed, g) {
		fold = "▸"
	}
	span := 0
	for _, w := range laneCols[:5] {
		span += w + 1
	}
	title := padRight(ansi.Truncate(sSection.Render(fold+" "+g)+"  "+sDim.Render(plural(len(lanes), "lane")), span-1, "…"), span-1)
	bar, frac := scopeCells(done, total)
	rest := laneCells(5, bar, frac, sDim.Render(fmt.Sprintf("%d of %s", allowed, plural(len(lanes), "lane"))))
	return cursor + " " + title + " " + rest
}

const laneBarCols = 20

// paneStates are the lanes table's state symbols: herdr's working, blocked and idle, then stalled and no pane.
var paneStates = []struct{ name, sym string }{
	{"working", sOK.Render("●")},
	{"blocked", sErr.Render("■")},
	{"idle", sDim.Render("○")},
	{"stalled", sWarn.Render("◐")},
	{"gone", sDim.Render("·")},
}

// lanePrio colours a lane name by its highest open priority: P0-P3 each get a colour, lower is dim grey.
func lanePrio(p int) lipgloss.Style {
	c := []color.Color{cRed, cYellow, cMagenta, cBlue}
	if p < 0 || p >= len(c) {
		return lipgloss.NewStyle().Foreground(cGray).Reverse(true).Faint(true)
	}
	return lipgloss.NewStyle().Bold(p <= 1).Foreground(c[p]).Reverse(true)
}

func (d *Dispatch) renderQueue(room int) []string {
	v := d.selLane()
	if v == nil {
		if r, ok := d.selRow(); ok {
			_, byGroup := d.groups()
			return []string{sSection.Render(r.group + " group"), sDim.Render("  " + strings.Join(byGroup[r.group], ", "))}
		}
		return nil
	}
	title := sSection.Render(v.Lane.Name+" queue") + sDim.Render(fmt.Sprintf("  %d queued · %d waiting on deps · %d partial · %d working · %d done",
		v.Count[dispatch.Queued], v.Count[dispatch.Waiting], v.Count[dispatch.Partial], v.Count[dispatch.InProgress], v.Count[dispatch.Completed]))
	out := []string{title}
	if len(v.Queue) == 0 {
		return append(out, sDim.Render("  nothing queued"))
	}
	start := clamp(d.bead-room/2, 0, max(0, len(v.Queue)-room))
	for i, is := range v.Queue[start:min(len(v.Queue), start+room)] {
		i += start
		mark, why := sDim.Render("·"), ""
		if d.rep.Allow.Permits(d.rep.Snap, is, v.Lane.Name) {
			mark, why = sOK.Render("✓"), d.allowedBy(is, v.Lane.Name)
		}
		tag := ""
		if is.HasLabel(dispatch.LabelPartial) {
			tag = sWarn.Render(" partial")
		}
		line := fmt.Sprintf("  %s %s %s%s %s", mark, padRight(ansi.Truncate(is.ID, 18, "…"), 18), is.Title, tag, sDim.Render(why))
		if i == d.bead && d.inQueue {
			line = sSel.Render("▶" + line[1:])
		}
		out = append(out, line)
	}
	return out
}

// renderLedgers lists the live ledgers above the selected lane's (or group's) active and queued
// beads: shared records any of those lanes may update, each with its latest log entry.
func (d *Dispatch) renderLedgers() []string {
	r, ok := d.selRow()
	if !ok {
		return nil
	}
	lanes := []string{r.lane}
	if r.lane == "" {
		_, byGroup := d.groups()
		lanes = byGroup[r.group]
	}
	var ids []string
	for _, name := range lanes {
		v := d.rep.Lanes[name]
		beads := v.Queue
		if is := d.rep.Snap.ByID[v.Active]; is != nil {
			beads = append([]*bd.Issue{is}, beads...)
		}
		for _, is := range beads {
			for _, id := range dispatch.Ledgers(d.rep.Snap, is) {
				if !slices.Contains(ids, id) {
					ids = append(ids, id)
				}
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	out := []string{sSection.Render("live ledgers") + sDim.Render("  shared by every lane, written with bt ledger checkout/commit")}
	for _, id := range ids {
		is := d.rep.Snap.ByID[id]
		last := sDim.Render("no log yet")
		if l := bd.ParseLedger(is.Description).Log; len(l) > 0 {
			e := l[len(l)-1]
			last = sDim.Render(dayAge(e.Date) + ": " + e.Text)
		}
		out = append(out, fmt.Sprintf("  %s %s  %s", padRight(ansi.Truncate(id, 18, "…"), 18), is.Title, last))
	}
	return append(out, sRule.Render(strings.Repeat("─", d.w)))
}

func (d *Dispatch) allowedBy(is *bd.Issue, lane string) string {
	a := d.rep.Allow
	switch {
	case slices.Contains(a.Beads, is.ID):
		return "(bead)"
	case slices.Contains(a.Lanes, lane):
		return "(lane)"
	}
	return "(epic)"
}

func (d *Dispatch) hints() string {
	if d.msg != "" {
		return sWarn.Render(d.msg)
	}
	k := func(key, what string) string { return sKey.Render(key) + " " + sDim.Render(what) }
	if d.graphOn {
		return strings.Join([]string{k("hjkl", "move"), k("H", "closed"), k("g/esc", "back")}, "  ")
	}
	if d.inQueue {
		return strings.Join([]string{k("space", "allow bead"), k("e", "allow its epic"), k("g", "graph"), k("tab", "lanes"), k("E", "edit dispatch.toml"), k("q", "quit")}, "  ")
	}
	if r, ok := d.selRow(); ok && r.lane == "" {
		return strings.Join([]string{k("enter", "fold group"), k("space", "allow all its lanes"), k("j/k", "move"), k("E", "edit dispatch.toml"), k("q", "quit")}, "  ")
	}
	return strings.Join([]string{k("space", "allow whole lane"), k("g", "graph"), k("tab", "queue"), k("j/k", "move"), k("D", "delete lane"), k("E", "edit dispatch.toml"), k("q", "quit")}, "  ")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
