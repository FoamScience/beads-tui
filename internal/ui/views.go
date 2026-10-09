package ui

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/charmbracelet/x/ansi"
)

// listView is the common shape: rows built from the snapshot, shared list navigation.
type listView struct {
	issueList
	empty string
	bar   bool // draw the machine filter line above the list
}

func (v *listView) Selected() *bd.Issue { return v.issueList.Selected() }

func (v *listView) Render(a *App, w, h int) string {
	if v.bar {
		bar := ansi.Truncate(fmt.Sprintf(" %s %s", sDim.Render("machine"), machineHint(a)), w, "…")
		return bar + "\n\n" + v.body(a, w, h-2)
	}
	return v.body(a, w, h)
}

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
				right:  progressBar(done, total, 8) + sDim.Render(fmt.Sprintf(" %d/%d", done, total)),
			}
		}
		rows = append(rows, h)
		for _, is := range groups[k] {
			rows = append(rows, row{issue: is, indent: 1})
		}
	}
	return rows
}

const allMachines = "all"

func (a *App) machineFilter() string {
	if a.state.Machine == "" {
		return a.host
	}
	return a.state.Machine
}

// machineMatch is the one machine filter every tab applies: beads labelled for the selected
// machine, plus beads with no machine label, which belong to nobody and so show everywhere.
func (a *App) machineMatch(is *bd.Issue) bool {
	f := a.machineFilter()
	m := is.LabelWithPrefix("machine:")
	return f == allMachines || m == f || m == ""
}

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

func newNow() *nowView { return &nowView{listView{empty: "nothing active for this filter"}} }

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
	n := 0
	for _, r := range v.rows {
		if r.issue != nil && !r.isHeader() {
			n++
		}
	}
	bar := fmt.Sprintf(" %s %s   %s %s", sDim.Render("status"), strings.Join(chips, " "), sDim.Render("machine"), machineHint(a))
	right := sDim.Render(fmt.Sprintf("%d active ", n))
	bar = ansi.Truncate(bar, w-len(right)-1, "…")
	bar += strings.Repeat(" ", max(w-ansi.StringWidth(bar)-ansi.StringWidth(right), 1)) + right
	return bar + "\n\n" + v.listView.Render(a, w, h-2)
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

// ---- Ready ----

type readyView struct{ listView }

func newReady() *readyView { return &readyView{listView{empty: "nothing ready for this filter"}} }

func (v *readyView) Name() string { return "Ready" }

// ready mirrors bd ready: open, not deferred into the future, no open blocker on itself or an ancestor.
func ready(s *bd.Snapshot, is *bd.Issue) bool {
	if is.Status != "open" || is.IssueType == "epic" {
		return false
	}
	if is.DeferUntil != nil && is.DeferUntil.After(time.Now()) {
		return false
	}
	for p := is; p != nil; p = s.ByID[p.Parent] {
		if s.IsBlocked(p) {
			return false
		}
	}
	return true
}

func (v *readyView) Rebuild(a *App) {
	q := strings.ToLower(a.state.ReadyFilter)
	var list []*bd.Issue
	for _, is := range a.snap.Issues {
		if !ready(a.snap, is) || !a.machineMatch(is) {
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
		a.modal = newPrompt("Filter ready (label, epic, title; empty clears)", a.state.ReadyFilter, func(s string) tea.Cmd {
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
	bar := fmt.Sprintf(" %s %s   %s %s   %s", sDim.Render("filter"), f, sDim.Render("machine"), machineHint(a), sDim.Render(fmt.Sprintf("%d ready", n)))
	return ansi.Truncate(bar, w, "…") + "\n\n" + v.listView.Render(a, w, h-2)
}

func (v *readyView) Hints() []string { return []string{"f filter", "M machine", "C claim", "a new"} }

// ---- Activity ----

type activityView struct{ listView }

func newActivity() *activityView { return &activityView{listView{empty: "no activity", bar: true}} }

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

func (v *activityView) Hints() []string { return []string{"[/] day"} }

// ---- Triage ----

type triageView struct{ listView }

func newTriage() *triageView { return &triageView{listView{empty: "all clean", bar: true}} }

func (v *triageView) Name() string { return "Triage" }

func (v *triageView) Rebuild(a *App) {
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

func (v *triageView) Hints() []string { return []string{"[/] rule", "m machine", "x ref", "e est"} }

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
	bar := fmt.Sprintf(" %s %s   %s", sDim.Render("machine"), machineHint(a), sDim.Render(fmt.Sprintf("%d %s", n, what)))
	return ansi.Truncate(bar, w, "…") + "\n\n" + v.listView.Render(a, w, h-2)
}

func (v *inboxView) Hints() []string {
	return []string{"r respond", "X dismiss", "M machine", "H handled"}
}
