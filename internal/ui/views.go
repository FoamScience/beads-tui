package ui

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
)

// listView is the common shape: rows built from the snapshot, shared list navigation.
type listView struct {
	issueList
	empty string
}

func (v *listView) Selected() *bd.Issue { return v.issueList.Selected() }

func (v *listView) Render(a *App, w, h int) string { return v.body(a, w, h) }

func (v *listView) body(a *App, w, h int) string {
	if a.loading && len(a.snap.Issues) == 0 {
		return sDim.Render("\n  loading issues…")
	}
	if len(v.rows) == 0 {
		return sDim.Render("\n  " + v.empty)
	}
	return v.issueList.Render(a, w, h)
}

func byPriority(is []*bd.Issue) {
	sort.SliceStable(is, func(i, j int) bool {
		if is[i].Priority != is[j].Priority {
			return is[i].Priority < is[j].Priority
		}
		return bd.IDLess(is[i].ID, is[j].ID)
	})
}

// groupByEpic turns issues into epic-headed groups, ordered by the best priority inside each group.
func groupByEpic(a *App, issues []*bd.Issue) []row {
	groups := map[string][]*bd.Issue{}
	for _, is := range issues {
		key := ""
		if is.IssueType == "epic" {
			key = is.ID
			if _, ok := groups[key]; !ok {
				groups[key] = nil
			}
			continue
		}
		if e := a.snap.Epic(is); e != nil {
			key = e.ID
		}
		groups[key] = append(groups[key], is)
	}
	keys := make([]string, 0, len(groups))
	for k, g := range groups {
		byPriority(g)
		keys = append(keys, k)
	}
	best := func(k string) int {
		if g := groups[k]; len(g) > 0 {
			return g[0].Priority
		}
		return a.snap.ByID[k].Priority
	}
	sort.Slice(keys, func(i, j int) bool {
		if (keys[i] == "") != (keys[j] == "") {
			return keys[j] == ""
		}
		if bi, bj := best(keys[i]), best(keys[j]); bi != bj {
			return bi < bj
		}
		return keys[i] < keys[j]
	})
	var rows []row
	for i, k := range keys {
		if i > 0 {
			rows = append(rows, row{spacer: true})
		}
		h := row{header: sDim.Render("(no epic)")}
		if e := a.snap.ByID[k]; e != nil {
			done, total := a.snap.Progress(k)
			h = row{
				issue:  e,
				header: epicBadge(a, e),
				right:  progressBar(done, total, 8) + sDim.Render(fmt.Sprintf(" %3d/%-3d", done, total)), // fixed width keeps every epic bar in one column
			}
		}
		rows = append(rows, h)
		for _, is := range groups[k] {
			rows = append(rows, row{issue: is, indent: 1})
		}
	}
	return rows
}

const allMachines = bd.AllMachines

func (a *App) machineFilter() string {
	if a.state.Machine == "" {
		return a.host
	}
	return a.state.Machine
}

// machineMatch is the one machine filter every tab applies.
func (a *App) machineMatch(is *bd.Issue) bool { return is.OnMachine(a.machineFilter()) }

// machineChip is the machine filter as every tab's bar opens with it.
func machineChip(a *App) string { return sDim.Render(icMachine) + " " + machineHint(a) }

func machineHint(a *App) string {
	f := a.machineFilter()
	if f == allMachines {
		return "all machines"
	}
	h := strings.TrimPrefix(f, a.user+"-")
	if f == a.host {
		h += " (this)"
	}
	return h
}

// ---- Now ----

type nowView struct{ listView }

func newNow() *nowView {
	return &nowView{listView{empty: "nothing active for this filter", issueList: issueList{ageW: len(updatedLabel), goals: true}}}
}

func (v *nowView) Name() string { return "Now" }

func (v *nowView) Rebuild(a *App) {
	var active []*bd.Issue
	for _, is := range a.snap.Issues {
		if slices.Contains(a.state.NowStatuses, is.Status) && a.machineMatch(is) {
			active = append(active, is)
		}
	}
	rows := groupByEpic(a, active)
	for i := range rows {
		rows[i].agent = true
	}
	v.SetRows(rows)
}

func (v *nowView) Update(a *App, k tea.KeyPressMsg) (bool, tea.Cmd) {
	switch k.String() {
	case "f":
		var opts []option
		checked := map[string]bool{}
		for _, s := range statuses {
			opts = append(opts, option{s, statusGlyph[s] + " " + s})
			checked[s] = slices.Contains(a.state.NowStatuses, s)
		}
		a.modal = newMultiPicker("Statuses shown in Now", opts, checked, func(vals []string) tea.Cmd {
			a.state.NowStatuses = vals
			v.Rebuild(a)
			return saveState(a)
		})
		return true, nil
	}
	return v.HandleKey(k.String()), nil
}

func (v *nowView) Render(a *App, w, h int) string {
	var chips []string
	for _, s := range statuses {
		if slices.Contains(a.state.NowStatuses, s) {
			chips = append(chips, sChipOn.Render(s))
		}
	}
	top := header(a, w, sDim.Render(icStatus)+" "+strings.Join(chips, " "))
	right := v.columnLabels(w)
	labels := padLeft(right, w)
	return strings.Join(top, "\n") + "\n" + labels + "\n" + v.listView.Render(a, w, h-len(top)-1)
}

func (v *nowView) Hints() []string { return []string{"f statuses", "M machine", "[/] group"} }

// machinePicker offers this host, all machines, and every machine label in the DB.
func machinePicker(a *App) *modal {
	opts := []option{{"", "this machine (" + strings.TrimPrefix(a.host, a.user+"-") + ")"}, {allMachines, "all machines"}}
	for _, l := range a.knownLabels("machine:") {
		if m := strings.TrimPrefix(l, "machine:"); m != a.host {
			opts = append(opts, option{m, strings.TrimPrefix(m, a.user+"-")})
		}
	}
	return newPicker("Show issues for", opts, func(m string) tea.Cmd {
		a.state.Machine = m
		a.rebuild()
		return saveState(a)
	})
}

func saveState(a *App) tea.Cmd {
	if err := a.state.save(); err != nil {
		return flashErr(err)
	}
	return nil
}

// ---- Backlog ----

type readyView struct{ listView }

func newReady() *readyView {
	return &readyView{listView{empty: "nothing ready for this filter", issueList: issueList{goals: true}}}
}

func (v *readyView) Name() string { return "Backlog" }

// ready mirrors bd ready: open, not deferred into the future, no open blocker on itself or an ancestor.
func (v *readyView) Rebuild(a *App) {
	q := strings.ToLower(a.state.ReadyFilter)
	var list []*bd.Issue
	for _, is := range a.snap.Issues {
		if !a.snap.Ready(is) || !a.machineMatch(is) {
			continue
		}
		if q != "" {
			hay := strings.ToLower(is.ID + " " + is.Title + " " + strings.Join(is.Labels, " "))
			if e := a.snap.Epic(is); e != nil {
				hay += " " + strings.ToLower(e.ID+" "+e.Title)
			}
			if !strings.Contains(hay, q) {
				continue
			}
		}
		list = append(list, is)
	}
	v.SetRows(groupByEpic(a, list))
}

func (v *readyView) Update(a *App, k tea.KeyPressMsg) (bool, tea.Cmd) {
	switch k.String() {
	case "f":
		a.modal = newPrompt("Filter backlog (label, epic, title; empty clears)", a.state.ReadyFilter, func(s string) tea.Cmd {
			a.state.ReadyFilter = s
			v.Rebuild(a)
			return saveState(a)
		})
		return true, nil
	}
	return v.HandleKey(k.String()), nil
}

func (v *readyView) Render(a *App, w, h int) string {
	n := 0
	for _, r := range v.rows {
		if r.issue != nil && !r.isHeader() {
			n++
		}
	}
	f := a.state.ReadyFilter
	if f == "" {
		f = sDim.Render("none")
	}
	top := header(a, w, sDim.Render("filter ")+f, sDim.Render(fmt.Sprintf("%d ready", n)))
	return strings.Join(top, "\n") + "\n" + v.listView.Render(a, w, h-len(top))
}

func (v *readyView) Hints() []string { return []string{"f filter", "M machine", "C claim", "a new"} }

// ---- Activity ----

type activityView struct{ listView }

func newActivity() *activityView { return &activityView{listView{empty: "no activity"}} }

func (v *activityView) Name() string { return "Activity" }

func (v *activityView) Rebuild(a *App) {
	var list []*bd.Issue
	for _, is := range a.snap.Issues {
		if a.machineMatch(is) {
			list = append(list, is)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].UpdatedAt.After(list[j].UpdatedAt) })
	list = list[:min(len(list), 300)]
	var rows []row
	v.noteW = 0
	if fs := a.forgotten(); len(fs) > 0 {
		v.noteW = 14 // fits the reasons, e.g. "abandoned +12"
		rows = append(rows, row{header: sSection.Render("Needs attention"), right: sDim.Render("R resumes")})
		for _, f := range fs {
			rows = append(rows, row{issue: f.is, note: f.why})
		}
		rows = append(rows, row{spacer: true})
	}
	day := ""
	for _, is := range list {
		if d := dayLabel(is.UpdatedAt); d != day {
			if day != "" {
				rows = append(rows, row{spacer: true})
			}
			day = d
			rows = append(rows, row{header: sSection.Render(d)})
		}
		note := "updated"
		if is.Closed() && is.ClosedAt != nil && is.ClosedAt.Sub(is.UpdatedAt).Abs() < time.Minute {
			note = "closed"
		} else if is.UpdatedAt.Sub(is.CreatedAt) < time.Minute {
			note = "created"
		}
		rows = append(rows, row{issue: is, note: note})
	}
	v.SetRows(rows)
}

func dayLabel(t time.Time) string {
	t = t.Local()
	now := time.Now()
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return "Today"
	case now.AddDate(0, 0, -1).Format("2006-01-02") == t.Format("2006-01-02"):
		return "Yesterday"
	}
	return t.Format("Mon 2 Jan")
}

func (v *activityView) Update(a *App, k tea.KeyPressMsg) (bool, tea.Cmd) {
	return v.HandleKey(k.String()), nil
}

func (v *activityView) Render(a *App, w, h int) string {
	var chips []string
	if p := a.pulseHeader(); p != "" {
		chips = append(chips, p)
	}
	top := header(a, w, chips...)
	strip := a.renderAgents(w, h-len(top))
	return strings.Join(top, "\n") + "\n" + strip + v.body(a, w, h-len(top)-strings.Count(strip, "\n"))
}

func (v *activityView) Hints() []string { return []string{"[/] day"} }

// ---- Triage ----

type triageView struct {
	listView
	health bd.Hygiene
}

func newTriage() *triageView { return &triageView{listView: listView{empty: "all clean"}} }

func (v *triageView) Name() string { return "Triage" }

func (v *triageView) Rebuild(a *App) {
	mine := slices.DeleteFunc(slices.Clone(a.snap.Issues), func(is *bd.Issue) bool { return !a.machineMatch(is) })
	v.health = a.snap.Measure(mine, time.Now())
	var rows []row
	for _, r := range a.snap.Triage() {
		r.Issues = slices.DeleteFunc(r.Issues, func(is *bd.Issue) bool { return !a.machineMatch(is) })
		if len(r.Issues) == 0 {
			continue
		}
		right := sDim.Render(fmt.Sprintf("%d", len(r.Issues)))
		if r.Key != "" {
			right = sDim.Render("fix ") + sKey.Render(r.Key) + "  " + right
		}
		if len(rows) > 0 {
			rows = append(rows, row{spacer: true})
		}
		rows = append(rows, row{header: sWarn.Render("⚠ ") + sBold.Render(r.Name), right: right})
		for _, is := range r.Issues {
			rows = append(rows, row{issue: is, indent: 1})
		}
	}
	v.SetRows(rows)
}

func (v *triageView) Update(a *App, k tea.KeyPressMsg) (bool, tea.Cmd) {
	return v.HandleKey(k.String()), nil
}

// Render puts the hygiene metrics of the filtered beads above the rule violations, as a status
// card and a health card. The cards take at most a third of the height: past that the status card goes.
func (v *triageView) Render(a *App, w, h int) string {
	lines := header(a, w)
	if v.health.Total > 0 {
		budget := max(h/3, 3)
		lines = cards(w, budget, card{machineChip(a), v.summaryChips()}, card{sDim.Render("health"), v.problemChips()})
		if len(lines) > budget {
			lines = cards(w, budget, card{machineChip(a) + sDim.Render(" · health"), v.problemChips()})
		}
	}
	return strings.Join(lines, "\n") + "\n" + v.body(a, w, h-len(lines)-1)
}

func (v *triageView) summaryChips() []string {
	hy := v.health
	chips := []string{
		sDim.Render(icBeads) + fmt.Sprintf(" %d beads", hy.Total),
		sOK.Render(statusGlyph["closed"]) + fmt.Sprintf(" %d%% closed", pct(hy.Closed, hy.Total)),
	}
	for _, s := range []struct {
		n     int
		st    string
		style lipgloss.Style
	}{
		{hy.Open, "open", sDim},
		{hy.InProgress, "in_progress", sWarn},
		{hy.Blocked, "blocked", sErr},
		{hy.Deferred, "deferred", sBlue},
		{hy.Pinned, "pinned", sDim},
	} {
		if s.n > 0 {
			chips = append(chips, s.style.Render(statusGlyph[s.st])+fmt.Sprintf(" %d %s", s.n, strings.ReplaceAll(s.st, "_", " ")))
		}
	}
	return chips
}

// problemChips go green at zero and yellow otherwise; P0/P1 is a load figure, not a violation.
func (v *triageView) problemChips() []string {
	hy, act := v.health, v.health.Active()
	chip := func(icon string, n int, label string) string {
		st := sOK
		if n > 0 {
			st = sWarn
		}
		return st.Render(icon) + " " + st.Render(fmt.Sprintf("%d", n)) + " " + label
	}
	return []string{
		chip(icStale, hy.Stale30, fmt.Sprintf("stale 30d+ %s", sDim.Render(fmt.Sprintf("%d%%", pct(hy.Stale30, act))))),
		chip(icWaitBg, hy.Idle14, fmt.Sprintf("idle 14d+ %s", sDim.Render(fmt.Sprintf("%d%%", pct(hy.Idle14, act))))),
		chip(icEstimate, hy.NoEstimate, "no estimate"),
		chip(icUnowned, hy.Unassigned, "in progress unassigned"),
		chip(icNoDescr, hy.NoDescr, "no description"),
		sDim.Render(icPriority) + fmt.Sprintf(" %d P0/P1 %s", hy.HighPriority, sDim.Render(fmt.Sprintf("%d%%", pct(hy.HighPriority, act)))),
	}
}

func pct(n, of int) int {
	if of == 0 {
		return 0
	}
	return n * 100 / of
}

func (v *triageView) Hints() []string {
	return []string{"[/] rule", "M machine", "m set machine", "x ref", "e est"}
}

// ---- Inbox ----

// human beads are ones an agent flagged for the user with the "human" label (bd human list).
func isHuman(is *bd.Issue) bool { return is.HasLabel("human") }

// pendingHuman counts open human beads under the shared machine filter, like the Inbox shows them.
func (a *App) pendingHuman() int {
	n := 0
	for _, is := range a.snap.Issues {
		if isHuman(is) && !is.Closed() && a.machineMatch(is) {
			n++
		}
	}
	return n
}

type inboxView struct {
	listView
	showClosed bool
}

func newInbox() *inboxView { return &inboxView{listView: listView{empty: "nothing waiting on you"}} }

func (v *inboxView) Name() string { return "Inbox" }

func (v *inboxView) Rebuild(a *App) {
	var list []*bd.Issue
	for _, is := range a.snap.Issues {
		if isHuman(is) && (v.showClosed || !is.Closed()) && a.machineMatch(is) {
			list = append(list, is)
		}
	}
	v.SetRows(groupByEpic(a, list))
}

func (v *inboxView) Update(a *App, k tea.KeyPressMsg) (bool, tea.Cmd) {
	is := v.Selected()
	switch k.String() {
	case "H":
		v.showClosed = !v.showClosed
		v.Rebuild(a)
		return true, nil
	case "r", "X":
		if is == nil || is.Closed() || !isHuman(is) {
			return true, flash("select a pending human bead")
		}
		id := is.ID
		if k.String() == "r" {
			a.modal = newPrompt("Respond to "+a.shortID(id)+" (adds a comment and closes it)", "", func(t string) tea.Cmd {
				if t == "" {
					return flash("empty response, nothing sent")
				}
				return a.write("responded to "+a.shortID(id), a.mut(id, func(i *bd.Issue) { i.Status = "closed" }), "human", "respond", id, "--", t)
			})
		} else {
			a.modal = newPrompt("Dismiss "+a.shortID(id)+": reason (optional)", "", func(t string) tea.Cmd {
				args := []string{"human", "dismiss", id}
				if t != "" {
					args = append(args, "--", t)
				}
				return a.write("dismissed "+a.shortID(id), a.mut(id, func(i *bd.Issue) { i.Status = "closed" }), args...)
			})
		}
		return true, nil
	}
	return v.HandleKey(k.String()), nil
}

func (v *inboxView) Render(a *App, w, h int) string {
	n := 0
	for _, r := range v.rows {
		if r.issue != nil && !r.isHeader() {
			n++
		}
	}
	what := "waiting on you"
	if v.showClosed {
		what = "incl. handled"
	}
	top := header(a, w, sDim.Render(fmt.Sprintf("%d %s", n, what)))
	return strings.Join(top, "\n") + "\n" + v.listView.Render(a, w, h-len(top))
}

func (v *inboxView) Hints() []string {
	return []string{"r respond", "X dismiss", "M machine", "H handled"}
}
