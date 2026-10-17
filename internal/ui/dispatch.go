package ui

import (
	"cmp"
	"fmt"
	"image/color"
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
}

// laneRow is a line of the lanes table: a group header when lane is "", else one lane.
type laneRow struct{ group, lane string }

type stepMsg dispatch.Report

func NewDispatch(e *dispatch.Engine) *Dispatch { return &Dispatch{eng: e, state: loadState()} }

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
	case tea.KeyPressMsg:
		d.msg = ""
		if d.modal != nil {
			done, cmd := d.modal.update(msg)
			if done {
				d.modal = nil
			}
			return d, cmd
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

func (d *Dispatch) View() tea.View {
	v := tea.NewView(d.render())
	v.AltScreen = true
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
	if d.rep.Cfg == nil {
		out = append(out, sWarn.Render("waiting for the first tick (or lanes.toml failed, see events)"))
	} else {
		out = append(out, d.renderLanes()...)
		out = append(out, sRule.Render(strings.Repeat("─", d.w)))
		out = append(out, d.renderQueue()...)
	}
	out = append(out, sRule.Render(strings.Repeat("─", d.w)))
	footer := d.hints()
	if room := d.h - len(out) - 1; room > 0 {
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

// laneCols are the lanes table's fixed widths: LANE PANE STATUS ACTIVE NEXT PROGRESS DONE ALLOWED.
var laneCols = []int{16, 7, 8, 17, 17, laneBarCols, 7, 12}

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

func (d *Dispatch) renderLanes() []string {
	out := []string{sDim.Render("  " + laneCells(0, "LANE", "PANE", "STATUS", "ACTIVE", "NEXT", "PROGRESS", "DONE", "ALLOWED"))}
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
		out = append(out, line)
		if r.lane != "" {
			out = append(out, "      "+laneFit(d.rep.Cfg.Lanes[r.lane]))
		}
	}
	legend := make([]string, len(laneStates))
	for i, s := range laneStates {
		legend[i] = s.style.Render("▰") + " " + sDim.Render(s.label)
	}
	out = append(out, "  "+strings.Join(legend, "  ")+"  "+sWarn.Render("stalled")+sDim.Render(" = idle pane holding a claimed bead"))
	return append(out, "  "+sDim.Render("lane colour = its highest open priority: ")+lanePrio(0).Render(" P0 ")+" "+lanePrio(1).Render(" P1 ")+" "+lanePrio(2).Render(" P2 ")+" "+lanePrio(3).Render(" P3 ")+" "+lanePrio(4).Render(" P4+ "))
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
	pane, status := "-", sDim.Render("gone")
	switch {
	case !v.Live:
	case v.Pane.Status == "idle" && v.Active != "":
		pane, status = v.Pane.ID, sWarn.Render("stalled")
	default:
		pane, status = v.Pane.ID, v.Pane.Status
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
	counts, done, total := laneCounts(v)
	return cursor + " " + laneCells(0, " "+name, pane, status, cmp.Or(v.Active, "-"), next,
		stackedBar(counts, laneBarCols), fmt.Sprintf("%d/%d", done, total), allow)
}

// groupLine sums the group's lanes into one bar; its title spans the LANE to NEXT columns.
func (d *Dispatch) groupLine(cursor, g string, lanes []string) string {
	sum := make([]int, len(laneStates))
	done, total, allowed := 0, 0, 0
	for _, l := range lanes {
		c, dn, tt := laneCounts(d.rep.Lanes[l])
		for i := range c {
			sum[i] += c[i]
		}
		done, total = done+dn, total+tt
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
	rest := laneCells(5, stackedBar(sum, laneBarCols), fmt.Sprintf("%d/%d", done, total), sDim.Render(fmt.Sprintf("%d of %s", allowed, plural(len(lanes), "lane"))))
	return cursor + " " + title + " " + rest
}

func laneCounts(v *dispatch.LaneView) (counts []int, done, total int) {
	counts = make([]int, len(laneStates))
	for i, s := range laneStates {
		counts[i] = v.Count[s.state]
		total += counts[i]
	}
	return counts, v.Count[dispatch.Completed], total
}

const laneBarCols = 20

// lanePrio colours a lane name by its highest open priority: P0-P3 each get a colour, lower is dim grey.
func lanePrio(p int) lipgloss.Style {
	c := []color.Color{cRed, cYellow, cMagenta, cBlue}
	if p < 0 || p >= len(c) {
		return lipgloss.NewStyle().Foreground(cGray).Reverse(true).Faint(true)
	}
	return lipgloss.NewStyle().Bold(p <= 1).Foreground(c[p]).Reverse(true)
}

// laneStates is the order and colour of a lane's progress bar segments.
var laneStates = []struct {
	state dispatch.Progress
	label string
	style lipgloss.Style
}{
	{dispatch.Completed, "done", sOK},
	{dispatch.InProgress, "working", lipgloss.NewStyle().Foreground(cBlue)},
	{dispatch.Partial, "partial", sWarn},
	{dispatch.Queued, "queued", lipgloss.NewStyle()},
	{dispatch.Waiting, "waiting on deps", sDim},
}

// stackedBar splits width cells across counts by largest remainder; a non-zero count always gets a cell when there is room.
func stackedBar(counts []int, width int) string {
	cells := barCells(counts, width)
	var b strings.Builder
	for i, n := range cells {
		b.WriteString(laneStates[i].style.Render(strings.Repeat("▰", n)))
	}
	used := 0
	for _, n := range cells {
		used += n
	}
	return b.String() + sDim.Render(strings.Repeat("▱", width-used))
}

func barCells(counts []int, width int) []int {
	total := 0
	for _, c := range counts {
		total += c
	}
	cells := make([]int, len(counts))
	if total == 0 {
		return cells
	}
	used := 0
	rem := make([]int, len(counts))
	for i, c := range counts {
		cells[i], rem[i] = c*width/total, c*width%total
		if c > 0 && cells[i] == 0 {
			cells[i], rem[i] = 1, -1
		}
		used += cells[i]
	}
	for used > width { // minimum cells overflowed: take back from the largest segment
		i := slices.Index(cells, slices.Max(cells))
		cells[i]--
		used--
	}
	for used < width {
		i := slices.Index(rem, slices.Max(rem))
		cells[i]++
		rem[i] = -1
		used++
	}
	return cells
}

func (d *Dispatch) renderQueue() []string {
	v := d.selLane()
	if v == nil {
		if r, ok := d.selRow(); ok {
			_, byGroup := d.groups()
			return []string{sSection.Render(r.group + " group"), sDim.Render("  " + strings.Join(byGroup[r.group], ", "))}
		}
		return nil
	}
	title := sSection.Render(v.Lane.Name + " queue")
	if v.Active != "" {
		title += sDim.Render("  working " + v.Active)
	}
	out := []string{title}
	if len(v.Queue) == 0 {
		return append(out, sDim.Render("  nothing queued"))
	}
	room := max(3, (d.h-2*len(d.rows())-8)/2)
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
	if d.inQueue {
		return strings.Join([]string{k("space", "allow bead"), k("e", "allow its epic"), k("tab", "lanes"), k("E", "edit dispatch.toml"), k("q", "quit")}, "  ")
	}
	if r, ok := d.selRow(); ok && r.lane == "" {
		return strings.Join([]string{k("enter", "fold group"), k("space", "allow all its lanes"), k("j/k", "move"), k("E", "edit dispatch.toml"), k("q", "quit")}, "  ")
	}
	return strings.Join([]string{k("space", "allow whole lane"), k("tab", "queue"), k("j/k", "move"), k("D", "delete lane"), k("E", "edit dispatch.toml"), k("q", "quit")}, "  ")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
