package ui

import (
	"cmp"
	"fmt"
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
	lane    int
	bead    int
	msg     string
	at      time.Time
	modal   *modal
}

type stepMsg dispatch.Report

func NewDispatch(e *dispatch.Engine) *Dispatch { return &Dispatch{eng: e} }

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

func (d *Dispatch) selLane() *dispatch.LaneView {
	names := d.lanes()
	if len(names) == 0 {
		return nil
	}
	d.lane = clamp(d.lane, 0, len(names)-1)
	return d.rep.Lanes[names[d.lane]]
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
	case "space":
		v := d.selLane()
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
	d.lane += n
	d.bead = 0
	d.selLane()
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

func (d *Dispatch) renderLanes() []string {
	const row = "%s %-14s %-7s %-8s %-16s %-16s %s %7s  %s"
	out := []string{sDim.Render(fmt.Sprintf(row, " ", "LANE", "PANE", "STATUS", "ACTIVE", "NEXT", padRight("PROGRESS", laneBarCols), "DONE", "ALLOWED"))}
	for i, name := range d.lanes() {
		v := d.rep.Lanes[name]
		pane, status := "-", "gone"
		if v.Live {
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
		cursor := " "
		if i == d.lane {
			cursor = sAccent.Render("▶")
		}
		counts := make([]int, len(laneStates))
		total := 0
		for i, s := range laneStates {
			counts[i] = v.Count[s.state]
			total += counts[i]
		}
		line := fmt.Sprintf(row, cursor, name, pane, status, cmp.Or(v.Active, "-"), next,
			stackedBar(counts, laneBarCols), fmt.Sprintf("%d/%d", v.Count[dispatch.Completed], total), allow)
		if v.Lane.Auto {
			line += sDim.Render("  auto")
		}
		if i == d.lane && !d.inQueue {
			line = sSel.Render(line)
		}
		out = append(out, line)
	}
	legend := make([]string, len(laneStates))
	for i, s := range laneStates {
		legend[i] = s.style.Render("▰") + " " + sDim.Render(s.label)
	}
	return append(out, "  "+strings.Join(legend, "  "))
}

const laneBarCols = 20

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
	room := max(3, (d.h-len(d.lanes())-8)/2)
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
		line := fmt.Sprintf("  %s %-18s %s%s %s", mark, is.ID, is.Title, tag, sDim.Render(why))
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
	return strings.Join([]string{k("space", "allow whole lane"), k("tab", "queue"), k("j/k", "move"), k("D", "delete lane"), k("E", "edit dispatch.toml"), k("q", "quit")}, "  ")
}
