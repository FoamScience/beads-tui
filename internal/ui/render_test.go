package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/charmbracelet/x/ansi"
)

func fixture() []bd.Issue {
	now := time.Now().UTC()
	started := now.Add(-5 * time.Hour)
	return []bd.Issue{
		{ID: "t-a", Title: "Epic A with a rather long title that must be truncated somewhere", IssueType: "epic", Status: "open", Priority: 1, UpdatedAt: now},
		{ID: "t-a.1", Parent: "t-a", Title: "Active child", IssueType: "task", Status: "in_progress", Priority: 0, UpdatedAt: now.Add(-72 * time.Hour), StartedAt: &started,
			Labels: []string{"machine:u-host"}, EstimatedMinutes: 90, Notes: "first note\nsecond note", Description: "# Heading\n\nbody"},
		{ID: "t-a.2", Parent: "t-a", Title: "Blocked child", IssueType: "bug", Status: "open", Priority: 2, UpdatedAt: now,
			Dependencies: []bd.Dependency{{IssueID: "t-a.2", DependsOnID: "t-a.1", Type: "blocks"}}},
		{ID: "t-a.3", Parent: "t-a", Title: "Ready child", IssueType: "task", Status: "open", Priority: 2, UpdatedAt: now},
		{ID: "t-b", Title: "Orphan done", IssueType: "task", Status: "closed", Priority: 3, UpdatedAt: now},
	}
}

func TestViewsFitTerminal(t *testing.T) {
	writeGoals(t)
	tabs := len(New(bd.Client{Dir: t.TempDir()}).views)
	for _, size := range [][2]int{{60, 15}, {80, 24}, {120, 40}, {160, 50}} {
		for tab := range tabs {
			for _, enter := range []bool{false, true} {
				name := fmt.Sprintf("%dx%d/tab%d/enter=%v", size[0], size[1], tab+1, enter)
				a := New(bd.Client{Dir: t.TempDir()})
				a.state, a.host = defaultState(), "u-host"
				a.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				a.Update(snapshotMsg{issues: fixture()})
				a.switchTo(tab)
				if g, ok := a.views[tab].(*graphView); ok {
					g.setRoot(a, a.snap.ByID["t-a.1"])
				}
				if enter {
					a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				}
				out := a.render()
				if head := ansi.Strip(strings.SplitN(out, "\n", 2)[0]); !strings.Contains(head, tabLabel(0, a.views[0])) || !strings.Contains(head, "@") {
					t.Errorf("%s: header lost tabs: %q", name, head)
				}
				if tab == 0 && !strings.Contains(out, "Active child") {
					t.Errorf("%s: Now shows no fixture rows", name)
				}
				lines := strings.Split(out, "\n")
				if len(lines) != size[1] {
					t.Errorf("%s: %d lines, want %d", name, len(lines), size[1])
				}
				for i, l := range lines {
					if w := lipgloss.Width(l); w > size[0] {
						t.Errorf("%s: line %d is %d wide: %q", name, i, w, l)
					}
				}
			}
		}
	}
}

func TestReadyAndBlocked(t *testing.T) {
	s := bd.NewSnapshot(fixture())
	if !s.IsBlocked(s.ByID["t-a.2"]) {
		t.Error("t-a.2 should be blocked by in-progress t-a.1")
	}
	if !s.Ready(s.ByID["t-a.3"]) || s.Ready(s.ByID["t-a.2"]) || s.Ready(s.ByID["t-a"]) {
		t.Error("ready: want only t-a.3")
	}
	if !bd.IDLess("t-a.2", "t-a.10") || bd.IDLess("t-a.10", "t-a.2") {
		t.Error("IDLess must compare segments numerically")
	}
}

func TestCloseParentOffersCascade(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state = defaultState()
	a.Update(snapshotMsg{issues: fixture(), gen: a.gen})
	a.closeFlow([]*bd.Issue{a.snap.ByID["t-a"]})
	if a.modal == nil || !strings.Contains(a.modal.title, "3 open descendant") {
		t.Fatalf("want cascade picker, got %+v", a.modal)
	}
	if a.modal.options[0].value != "cascade" {
		t.Fatalf("first option %q", a.modal.options[0].value)
	}
	if got := a.openDescendants("t-a"); len(got) != 3 || got[len(got)-1] == "t-a" {
		t.Fatalf("descendants %v", got)
	}
	a.modal = nil
	a.closeFlow([]*bd.Issue{a.snap.ByID["t-a.3"]})
	if a.modal == nil || a.modal.picker {
		t.Fatal("leaf without blockers should go straight to the reason prompt")
	}
}

func TestNoteKeepsExistingNotes(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.Update(snapshotMsg{issues: fixture(), gen: a.gen})
	a.override = a.snap.ByID["t-a.1"]
	a.focusDetail = true
	a.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	for _, r := range "third" {
		a.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := a.snap.ByID["t-a.1"].Notes; got != "first note\nsecond note\nthird" {
		t.Fatalf("notes after adding one: %q", got)
	}
}

func TestBulkEstimateCoversEveryMarkedIssue(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(snapshotMsg{issues: fixture(), gen: a.gen})
	a.marks = map[string]bool{"t-a.2": true, "t-a.3": true}
	a.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	for _, r := range "45" {
		a.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	for _, id := range []string{"t-a.2", "t-a.3"} {
		if got := a.snap.ByID[id].EstimatedMinutes; got != 45 {
			t.Errorf("%s estimate %d, want 45", id, got)
		}
	}
	if a.snap.ByID["t-a.1"].EstimatedMinutes != 90 || len(a.marks) != 0 {
		t.Error("unmarked issue changed or marks not cleared")
	}
}

func TestInboxAndAlerts(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(snapshotMsg{issues: fixture(), gen: a.gen})
	old := a.snap
	next := fixture()
	next[3].Labels = []string{"human", "machine:u-host"}            // t-a.3 flagged for the user, on this machine
	next[2].Labels = []string{"human", "machine:other"}             // t-a.2 flagged on another machine
	next[1].Status, next[1].ClosedAt = "closed", &next[1].UpdatedAt // in-progress t-a.1 closed
	a.setSnapshot(bd.NewSnapshot(next))
	ev := strings.Join(a.alertEvents(old), ",")
	if !strings.Contains(ev, "needs you: a.3") || !strings.Contains(ev, "closed: a.1") {
		t.Errorf("alerts: %q", ev)
	}
	if a.pendingHuman() != 1 {
		t.Errorf("pending human: %d", a.pendingHuman())
	}
	inbox := a.views[a.viewIndex("Inbox")].(*inboxView)
	if strings.Contains(ev, "a.2") {
		t.Errorf("alert for another machine's human bead: %q", ev)
	}
	inbox.HandleKey("j")
	if is := inbox.Selected(); is == nil || is.ID != "t-a.3" {
		t.Errorf("inbox selection: %+v", is)
	}
}

func TestDetailLinksOpenChildrenAndGoBack(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	a.Update(snapshotMsg{issues: fixture(), gen: a.gen})
	a.override, a.focusDetail = a.snap.ByID["t-a"], true
	a.render()
	if got := a.detail.links; len(got) < 3 {
		t.Fatalf("epic detail links: %v", got)
	}
	key := func(k tea.KeyPressMsg) { a.Update(k); a.render() }
	key(tea.KeyPressMsg{Code: tea.KeyTab})
	first := a.detail.selectedLink()
	key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if a.override == nil || a.override.ID != first {
		t.Fatalf("enter on link %q opened %+v", first, a.override)
	}
	key(tea.KeyPressMsg{Code: tea.KeyEscape})
	if a.override == nil || a.override.ID != "t-a" {
		t.Fatalf("esc should return to the epic, got %+v", a.override)
	}
	key(tea.KeyPressMsg{Code: tea.KeyEscape})
	if a.override != nil || a.focusDetail {
		t.Fatal("second esc should close the detail")
	}
}

func TestBoardMovesCardsBetweenColumns(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.state.Machine = allMachines
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	a.Update(snapshotMsg{issues: fixture(), gen: a.gen})
	a.active = a.viewIndex("Board")
	b := a.views[a.active].(*kanbanView)
	if is := b.Selected(); is == nil || is.ID != "t-a.1" {
		t.Fatalf("board should open on the in-progress card, got %+v", is)
	}
	a.Update(tea.KeyPressMsg{Code: 'H', Text: "H"})
	if got := a.snap.ByID["t-a.1"].Status; got != "blocked" {
		t.Fatalf("H moved the card to %q, want blocked", got)
	}
	if b.col != 1 {
		t.Fatalf("selection should follow the card to Blocked, col=%d", b.col)
	}
	if out := ansi.Strip(a.render()); !strings.Contains(out, "Blocked 2") {
		t.Fatalf("board header counts wrong:\n%s", out)
	}
}

func TestNowShowsLiveAgentForClaimedBead(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	issues := fixture()
	issues[1].Metadata = []byte(`{"claude_session.aaaa_bbbb":"w1:p1"}`)
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	a.Update(herdrMsg{panes: map[string]agentPane{"aaaa-bbbb": {Pane: "w1:p1", Status: "working", Title: "tm-tests", WSLabel: "infra"}}})
	if out := ansi.Strip(a.render()); !strings.Contains(out, icWorking+" tm-tests") {
		t.Fatalf("Now row lacks the live agent:\n%s", out)
	}
	a.Update(herdrMsg{panes: map[string]agentPane{}})
	a.Update(sessionNamesMsg{"aaaa-bbbb": {Name: "8010"}})
	if out := ansi.Strip(a.render()); !strings.Contains(out, icEnded+" ended 8010") {
		t.Fatalf("finished session not marked gone:\n%s", out)
	}
}

func TestInboxFollowsMachineFilter(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	issues := fixture()
	issues[3].Labels = []string{"human", "machine:u-host"}
	issues[2].Labels = []string{"human", "machine:other"}
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	count := func() int {
		n := 0
		for _, r := range a.views[a.viewIndex("Inbox")].(*inboxView).rows {
			if r.issue != nil && !r.isHeader() {
				n++
			}
		}
		return n
	}
	if count() != 1 || a.pendingHuman() != 1 {
		t.Fatalf("this machine: rows %d, badge %d, want 1", count(), a.pendingHuman())
	}
	a.state.Machine = allMachines
	a.rebuild()
	if count() != 2 || a.pendingHuman() != 2 {
		t.Fatalf("all machines: rows %d, badge %d, want 2", count(), a.pendingHuman())
	}
	issues[1].Labels = []string{"human"} // flagged without a machine: nobody's, so everyone's
	a.state.Machine = ""
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	if count() != 2 {
		t.Fatalf("unlabelled human bead hidden under this-machine filter: rows %d", count())
	}
}

func TestEveryTabFollowsMachineFilter(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	issues := fixture()
	issues = append(issues,
		bd.Issue{ID: "t-o", Title: "Other machine epic", IssueType: "epic", Status: "open", Labels: []string{"machine:other"}, UpdatedAt: time.Now()},
		bd.Issue{ID: "t-o.1", Parent: "t-o", Title: "Other machine task", IssueType: "task", Status: "in_progress", Labels: []string{"machine:other"}, UpdatedAt: time.Now().Add(-100 * time.Hour)},
	)
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	for _, name := range []string{"Now", "Backlog", "Triage", "Activity", "Inbox", "Board"} {
		v := a.views[a.viewIndex(name)]
		var ids []string
		if k, ok := v.(*kanbanView); ok {
			for _, col := range k.cards {
				for _, is := range col {
					ids = append(ids, is.ID)
				}
			}
		} else {
			for _, r := range rowsOf(v) {
				if r.issue != nil {
					ids = append(ids, r.issue.ID)
				}
			}
		}
		for _, id := range ids {
			if strings.HasPrefix(id, "t-o") {
				t.Errorf("%s shows %s from another machine", name, id)
			}
		}
	}
	// unlabelled beads (the whole fixture except t-a.1) still show, e.g. in Activity
	found := false
	for _, r := range rowsOf(a.views[a.viewIndex("Activity")]) {
		if r.issue != nil && r.issue.ID == "t-a.3" {
			found = true
		}
	}
	if !found {
		t.Error("unlabelled bead missing from Activity")
	}
	a.state.Machine = allMachines
	a.rebuild()
	found = false
	for _, r := range rowsOf(a.views[a.viewIndex("Triage")]) {
		if r.issue != nil && r.issue.ID == "t-o.1" {
			found = true
		}
	}
	if !found {
		t.Error("all machines: Triage should list the other machine's stale task")
	}
}

func TestTriageHygieneFollowsMachineFilter(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	issues := append(fixture(), bd.Issue{ID: "t-o", Title: "Other", IssueType: "task", Status: "open", Labels: []string{"machine:other"}, UpdatedAt: time.Now()})
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	v := a.views[a.viewIndex("Triage")].(*triageView)
	// the card block ends at the last bottom border
	cardsOf := func(out string) string {
		lines := strings.Split(ansi.Strip(out), "\n")
		end := 0
		for i, l := range lines {
			if strings.HasPrefix(l, "╰") {
				end = i + 1
			}
		}
		return strings.Join(lines[:end], "\n")
	}
	head := func(w int) string { return cardsOf(v.Render(a, w, 30)) }
	for _, want := range []string{"5 beads", "20% closed", "1 in progress", "1 blocked", "1 in progress unassigned"} {
		if !strings.Contains(head(200), want) {
			t.Errorf("this machine: %q missing from %q", want, head(200))
		}
	}
	for _, l := range strings.Split(head(40), "\n") {
		if lipgloss.Width(l) > 40 {
			t.Errorf("hygiene line wider than 40: %q", l)
		}
	}
	if !strings.Contains(head(200), "health") || strings.Count(strings.Split(head(200), "\n")[0], "╭") != 2 {
		t.Errorf("wide: status and health cards should sit side by side: %q", head(200))
	}
	short := cardsOf(v.Render(a, 40, 12))
	if n := strings.Count(short, "\n") + 1; n > 4 {
		t.Errorf("header takes %d of 12 rows: %q", n, short)
	}
	if !strings.Contains(short, "stale") {
		t.Errorf("short header dropped the problem chips: %q", short)
	}
	a.state.Machine = allMachines
	a.rebuild()
	if !strings.Contains(head(200), "6 beads") {
		t.Errorf("all machines: %q", head(200))
	}
}

func rowsOf(v View) []row {
	switch x := v.(type) {
	case *nowView:
		return x.rows
	case *readyView:
		return x.rows
	case *triageView:
		return x.rows
	case *activityView:
		return x.rows
	case *inboxView:
		return x.rows
	}
	return nil
}

func TestLiveTabRendersLedger(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	issues := append(fixture(), bd.Issue{
		ID: "t-l", Parent: "t-a", Title: "Campaign ledger", IssueType: "task", Status: "pinned",
		Labels: []string{"live", "machine:u-host"}, UpdatedAt: time.Now(),
		Description: "Tracks the campaign.\n\n= Current state =\n\n| Item | Value |\n|---|---|\n| Parametrized | 24 / 38 |\n\n= Log =\n\n2026-10-08: built\n2026-10-09: fixes landed\n",
		Design:      "Rerun with `make campaign`.",
	}, bd.Issue{ID: "t-m", Title: "Other machine ledger", Status: "pinned", Labels: []string{"live", "machine:other"}, UpdatedAt: time.Now()})
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	a.active = a.viewIndex("Live")
	out := ansi.Strip(a.render())
	for _, want := range []string{"Campaign ledger", "Current state", "Parametrized", "24 / 38", "Log (2)", "fixes landed", "Runbook", "1 live ledgers"} {
		if !strings.Contains(out, want) {
			t.Errorf("Live tab lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Other machine ledger") {
		t.Error("Live tab ignores the machine filter")
	}
	if i, j := strings.Index(out, "fixes landed"), strings.Index(out, "built"); i < 0 || j < 0 || i > j {
		t.Error("log should list the newest entry first")
	}
}

func TestMarkdownKeepsPlaceholders(t *testing.T) {
	d := newDetail()
	in := "## Provenance\n\nruns/<sim>_<attempt>/attempt.json, `--only <sims>`, a < b, snake_case_name, runs/<sim>_<attempt>/\n\n```\n<kept as is>\n```"
	out := ansi.Strip(d.markdown(in, 80))
	for _, want := range []string{"Provenance", "runs/<sim>_<attempt>/attempt.json", "--only <sims>", "a < b", "snake_case_name", "<kept as is>"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered markdown lost %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "## Provenance") || strings.Contains(out, `\<`) {
		t.Errorf("heading not rendered or escape leaked:\n%s", out)
	}
}

func TestLedgerLogRendersInlineMarkdown(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	issues := append(fixture(), bd.Issue{
		ID: "t-l", Title: "Ledger", Status: "pinned", Labels: []string{"live"}, UpdatedAt: time.Now(),
		Description: "Tracks.\n\n= Current state =\n\n| Item | Value |\n|---|---|\n| Tool | `make run` on **main** |\n\n= Log =\n\n2026-10-09: ran `collect` on runs/<sim>_<attempt>/, **24/38** done\n",
	})
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	a.active = a.viewIndex("Live")
	out := strings.Join(strings.Fields(ansi.Strip(a.render())), " ") // inline code is drawn padded
	for _, want := range []string{"ran collect on runs/<sim>_<attempt>/, 24/38 done", "make run on main"} {
		if !strings.Contains(out, want) {
			t.Errorf("ledger markdown not rendered, missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "**") || strings.Contains(out, "`collect`") {
		t.Errorf("raw markdown left in the ledger:\n%s", out)
	}
}

func TestDetailCountsApplyToTheNextMotionOnly(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	a.Update(snapshotMsg{issues: fixture(), gen: a.gen})
	a.override, a.focusDetail = a.snap.ByID["t-a"], true
	a.render()
	press := func(ks ...string) {
		for _, k := range ks {
			a.Update(tea.KeyPressMsg{Code: rune(k[0]), Text: k})
		}
	}
	press("5", "g", "g")
	if a.detail.pg.row != 4 {
		t.Fatalf("5gg should land on line 5, row %d", a.detail.pg.row)
	}
	press("g", "g", "3", "Z", "j")
	if a.detail.pg.row != 1 {
		t.Fatalf("a count before a non-motion key must not carry over, row %d", a.detail.pg.row)
	}
}

func TestWheelScrollsPaneUnderPointerElseMovesSelection(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.state.NowStatuses = []string{"open", "in_progress"}
	a.Update(tea.WindowSizeMsg{Width: 160, Height: 15}) // split, short enough for the detail to scroll
	a.Update(snapshotMsg{issues: fixture(), gen: a.gen})
	a.render()
	wheel := func(x int, b tea.MouseButton) {
		a.Update(tea.MouseWheelMsg{X: x, Y: 5, Button: b})
		a.render()
	}
	first := a.selected().ID
	wheel(5, tea.MouseWheelDown) // over the list
	if a.selected().ID == first {
		t.Fatal("wheel over the list should move to the next bead")
	}
	sel, off := a.selected().ID, a.detail.pg.off
	wheel(a.detailX+3, tea.MouseWheelDown) // over the detail pane
	if a.selected().ID != sel {
		t.Fatal("wheel over the detail pane must not change the selection")
	}
	if a.detail.pg.off == off && len(a.detail.pg.lines) > a.detail.pg.h {
		t.Fatalf("wheel over the detail pane should scroll it (off %d, %d lines in %d)", a.detail.pg.off, len(a.detail.pg.lines), a.detail.pg.h)
	}
}

func TestCtrlOTogglesMouseCapture(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	a.Update(snapshotMsg{issues: fixture(), gen: a.gen})
	if a.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("mouse should be captured by default")
	}
	a.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if !a.state.MouseOff || a.View().MouseMode != tea.MouseModeNone {
		t.Fatal("ctrl+o should hand the mouse back to the terminal")
	}
	a.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if a.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("a second ctrl+o should capture the mouse again")
	}
}

func TestClickOnTabSwitchesView(t *testing.T) {
	for _, w := range []int{160, 60} {
		a := New(bd.Client{Dir: t.TempDir()})
		a.state, a.host = defaultState(), "u-host"
		a.Update(tea.WindowSizeMsg{Width: w, Height: 30})
		a.Update(snapshotMsg{issues: fixture(), gen: a.gen})
		head := ansi.Strip(a.header())
		for i := len(a.views) - 1; i >= 0; i-- {
			label := tabLabel(i, a.views[i])
			at := strings.Index(head, label)
			if at < 0 {
				t.Fatalf("width %d: header lacks tab %q: %q", w, label, head)
			}
			x := lipgloss.Width(head[:at]) // icons are multibyte: click in columns, not bytes
			a.detailOpen = true
			a.Update(tea.MouseClickMsg{X: x, Y: 0, Button: tea.MouseLeft})
			if a.active != i || a.detailOpen {
				t.Fatalf("width %d: click at %d should open tab %d and leave the detail, active %d", w, x, i, a.active)
			}
			head = ansi.Strip(a.header())
		}
		a.Update(tea.MouseClickMsg{X: 3, Y: 4, Button: tea.MouseLeft})
		if a.active != 0 {
			t.Fatal("a click below the header must not switch tabs")
		}
	}
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	a.Update(snapshotMsg{issues: fixture(), gen: a.gen})
	a.render()
	a.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	a.render()
	a.Update(tea.MouseClickMsg{X: 20, Y: 0, Button: tea.MouseLeft})
	if a.active != 0 {
		t.Fatal("no tab bar is drawn below the minimum size, so clicks must not switch tabs")
	}
}

func TestActivityShowsAgentProgress(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	issues := fixture()
	meta := []byte(`{"claude_session.aaaa_bbbb":"w1:p1"}`)
	issues[1].Metadata, issues[2].Metadata, issues[4].Metadata = meta, meta, meta
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	a.active = a.viewIndex("Activity")
	a.Update(herdrMsg{panes: map[string]agentPane{"aaaa-bbbb": {Pane: "w1:p1", Status: "working", Title: "tm-tests"}}})
	if out := ansi.Strip(a.render()); !strings.Contains(out, "tm-tests  █") || !strings.Contains(out, "1/3") {
		t.Fatalf("agent strip lacks progress:\n%s", out)
	}
	a.Update(herdrMsg{panes: map[string]agentPane{}})
	if out := ansi.Strip(a.render()); !strings.Contains(out, icEnded+" aaaa-bbb") || !strings.Contains(out, "1/3") {
		t.Fatalf("ended recent agent missing from strip:\n%s", out)
	}
	stale := time.Now().Add(-30 * 24 * time.Hour)
	for i := range issues {
		issues[i].UpdatedAt = stale
	}
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	if out := ansi.Strip(a.render()); !strings.Contains(out, icEnded+" aaaa-bbb") {
		t.Fatalf("ended agent holding open beads dropped once stale:\n%s", out)
	}
	issues[1].Status, issues[2].Status = "closed", "closed"
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	if out := ansi.Strip(a.render()); strings.Contains(out, icEnded+" aaaa-bbb") {
		t.Fatalf("stale agent with every bead closed still listed:\n%s", out)
	}
}

func TestAgentStripPacksColumnsByWidth(t *testing.T) {
	panes := map[string]agentPane{}
	issues := fixture()
	for i, id := range []string{"aaaa", "bbbb", "cccc", "dddd"} {
		panes[id] = agentPane{Status: "idle", Title: "agent-" + id}
		issues[1+i%3].Metadata = []byte(fmt.Sprintf(`{"claude_session.%s":"p"}`, id))
		issues = append(issues, bd.Issue{ID: "t-s" + id, IssueType: "task", Status: "open", UpdatedAt: time.Now(),
			Metadata: []byte(fmt.Sprintf(`{"claude_session.%s":"p"}`, id))})
	}
	perLine := func(w int) int {
		a := New(bd.Client{Dir: t.TempDir()})
		a.state, a.host = defaultState(), "u-host"
		a.Update(tea.WindowSizeMsg{Width: w, Height: 40})
		a.Update(snapshotMsg{issues: issues, gen: a.gen})
		a.Update(herdrMsg{panes: panes})
		for _, l := range strings.Split(ansi.Strip(a.renderAgents(w, 36)), "\n") {
			if strings.Contains(l, "agent-") {
				return strings.Count(l, "agent-")
			}
		}
		return 0
	}
	if got := perLine(150); got != 3 {
		t.Errorf("150 cols: %d agents on the first line, want 3", got)
	}
	if got := perLine(90); got != 2 {
		t.Errorf("90 cols: %d agents on the first line, want 2", got)
	}
	if got := perLine(60); got != 1 {
		t.Errorf("60 cols: %d agents on the first line, want 1", got)
	}
}

func TestAgentStripGroupsBeadSessions(t *testing.T) {
	issues := fixture()
	issues[0].Metadata = []byte(`{"claude_session.orch":"p"}`)
	issues[1].Metadata = []byte(`{"claude_session.k1":"p"}`)
	issues[3].Metadata = []byte(`{"claude_session.k2":"p"}`)
	issues = append(issues,
		bd.Issue{ID: "t-c", IssueType: "epic", Status: "open", UpdatedAt: time.Now()},
		bd.Issue{ID: "t-c.1", Parent: "t-c", IssueType: "task", Status: "open", UpdatedAt: time.Now(),
			Metadata: []byte(`{"claude_session.k3":"p","lane":"infra"}`)})
	panes := map[string]agentPane{
		"orch": {Status: "working", Title: "boss"},
		"k1":   {Status: "working", Title: "t-a.1"},
		"k2":   {Status: "idle", Title: "work t-a.3 fix"},
		"k3":   {Status: "idle", Title: "t-c.1"},
	}
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 150, Height: 40})
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	a.Update(herdrMsg{panes: panes})
	lines := strings.Split(ansi.Strip(a.renderAgents(150, 36)), "\n")
	at := func(s string) int {
		for i, l := range lines {
			if strings.Contains(l, s) {
				return i
			}
		}
		t.Fatalf("%q missing from strip:\n%s", s, strings.Join(lines, "\n"))
		return -1
	}
	strip := strings.Join(lines, "\n")
	for _, hidden := range []string{"t-a.1", "t-a.3", "t-c.1"} {
		if strings.Contains(strip, hidden) {
			t.Errorf("bead session %s drawn in strip:\n%s", hidden, strip)
		}
	}
	if l := lines[at("boss")]; !strings.Contains(l, "0/3") {
		t.Errorf("orchestrator should count its bead sessions' beads (0/3): %q", l)
	}
	if l := lines[at(icLane+" infra")]; !strings.Contains(l, "0/1") {
		t.Errorf("lane cell should count its bead sessions' beads (0/1): %q", l)
	}
}

func TestGraphWrapsTitlesAndShowsSessions(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	issues := fixture()
	issues[1].Title = "Active child with a fairly long title that should wrap over several lines inside the box"
	issues[1].Metadata = []byte(`{"claude_session.aaaa_bbbb":"p"}`)
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	a.Update(herdrMsg{panes: map[string]agentPane{"aaaa-bbbb": {Status: "working", Title: "tm-tests"}}})
	a.active = a.viewIndex("Graph")
	a.views[a.active].(*graphView).setRoot(a, a.snap.ByID["t-a.1"])
	out := ansi.Strip(a.render())
	for _, want := range []string{"inside the box", icWorking + " tm-tests", "Ready child"} {
		if !strings.Contains(out, want) {
			t.Fatalf("graph lacks %q:\n%s", want, out)
		}
	}
}

func TestMemoriesTabRenders(t *testing.T) {
	for _, size := range [][2]int{{60, 15}, {160, 50}} {
		a := New(bd.Client{Dir: t.TempDir()})
		a.state, a.host = defaultState(), "u-host"
		a.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		a.Update(snapshotMsg{issues: fixture()})
		a.active = a.viewIndex("Memories")
		a.Update(memDataMsg{mems: map[string]string{
			"b-key":               "second memory",
			"@u-host:proj:c-slug": "tagged memory",
			"a-key":               strings.Repeat("a long memory that must wrap ", 20),
		}})
		out := a.render()
		plain := ansi.Strip(out)
		if strings.Contains(plain, "@u-host:proj:") || !strings.Contains(plain, "c-slug") || !strings.Contains(plain, " proj ") {
			t.Errorf("%dx%d: tags not shown as labels: %q", size[0], size[1], plain)
		}
		if !strings.Contains(out, "a-key") || !strings.Contains(out, "b-key") || !strings.Contains(out, "tagged memory") {
			t.Errorf("%dx%d: memories missing: %q", size[0], size[1], ansi.Strip(out))
		}
		lines := strings.Split(out, "\n")
		if len(lines) != size[1] {
			t.Errorf("%dx%d: %d lines", size[0], size[1], len(lines))
		}
		for i, l := range lines {
			if w := lipgloss.Width(l); w > size[0] {
				t.Errorf("%dx%d: line %d is %d wide", size[0], size[1], i, w)
			}
		}
	}
}

func TestEpicBarsShareAColumn(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	issues := fixture()
	issues = append(issues, bd.Issue{ID: "t-z", Title: "Big epic", IssueType: "epic", Status: "open", Priority: 1, UpdatedAt: time.Now()})
	for i := range 12 {
		issues = append(issues, bd.Issue{ID: fmt.Sprintf("t-z.%d", i+1), Parent: "t-z", Title: "kid", IssueType: "task", Status: "in_progress", UpdatedAt: time.Now()})
	}
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	cols := map[int]bool{}
	for _, l := range strings.Split(ansi.Strip(a.render()), "\n") {
		if i := strings.Index(l, "▰"); i >= 0 || strings.Contains(l, "▱") {
			if i < 0 {
				i = strings.Index(l, "▱")
			}
			cols[lipgloss.Width(l[:i])] = true
		}
	}
	if len(cols) != 1 {
		t.Fatalf("epic bars start in columns %v, want one", cols)
	}
}

func TestWorkflowsFromSnapshot(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	now := time.Now()
	issues := append(fixture(),
		bd.Issue{ID: "t-mol-1", Title: "done pour", IssueType: "molecule", Status: "open", UpdatedAt: now},
		bd.Issue{ID: "t-mol-2", Parent: "t-mol-1", Title: "step", IssueType: "task", Status: "closed", UpdatedAt: now},
		bd.Issue{ID: "t-mol-3", Title: "other pour", IssueType: "molecule", Status: "open", Labels: []string{"machine:other"}, UpdatedAt: now},
		bd.Issue{ID: "t-mol-4", Parent: "t-a", Title: "pour under an epic", IssueType: "molecule", Status: "open", UpdatedAt: now.Add(-time.Hour)},
		bd.Issue{ID: "t-mol-5", Parent: "t-mol-4", Title: "nested sub-molecule", IssueType: "molecule", Status: "open", UpdatedAt: now},
		bd.Issue{ID: "t-mol-9", Parent: "t-mol-gone", Title: "step of a burned root", IssueType: "task", Status: "open", UpdatedAt: now},
		bd.Issue{ID: "t-wisp-1", Title: "patrol", IssueType: "molecule", Status: "open", Ephemeral: true, UpdatedAt: now},
		bd.Issue{ID: "t-wisp-2", Parent: "t-wisp-1", Title: "wisp step", IssueType: "task", Status: "closed", Ephemeral: true, UpdatedAt: now},
		bd.Issue{ID: "t-wisp-8", Parent: "t-wisp-gone", Title: "step of a squashed wisp", IssueType: "task", Status: "open", Ephemeral: true, UpdatedAt: now},
	)
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	a.switchTo(a.viewIndex("Workflows"))
	v := a.views[a.active].(*workflowsView)
	if out := ansi.Strip(a.render()); strings.Contains(out, "formulas ·") || !strings.Contains(out, "loading formulas") {
		t.Fatalf("counts shown before the formulas loaded:\n%s", out)
	}
	v.Msg(a, molDataMsg{})
	sections := func() map[string][]string {
		m, cur := map[string][]string{}, ""
		for _, it := range v.items {
			if it.header != "" {
				cur = it.header
			} else if it.issue != nil {
				m[cur] = append(m[cur], it.issue.ID)
			}
		}
		return m
	}
	sec := sections()
	if got := fmt.Sprint(sec["Molecules"], sec["Wisps"]); got != "[t-mol-1 t-mol-4] [t-wisp-1]" {
		t.Fatalf("molecules and wisps = %s, want [t-mol-1 t-mol-4] [t-wisp-1]: no other machine, nested or orphan roots", got)
	}
	out := ansi.Strip(a.render())
	var bar string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "formulas ·") {
			bar = l
		}
	}
	if !strings.Contains(bar, "2 molecules · 1 wisp") || strings.Contains(bar, "1 wisps") || !strings.Contains(bar, "1 stale") {
		t.Fatalf("Workflows card wrong: %q", bar)
	}
	if !strings.Contains(out, "done pour 1/1 stale") {
		t.Fatalf("molecule with every step closed not flagged stale:\n%s", out)
	}
	if !strings.Contains(out, "patrol 1/1") || strings.Contains(out, "patrol 1/1 stale") {
		t.Fatalf("a wisp waiting for its squash is not stale:\n%s", out)
	}
	a.state.Machine = allMachines
	a.rebuild()
	if got := fmt.Sprint(sections()["Molecules"]); got != "[t-mol-1 t-mol-3 t-mol-4]" {
		t.Fatalf("all machines: molecules %s, want the other machine's pour too", got)
	}
}

func TestWispsStayOutOfOtherViews(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.state.Machine = allMachines
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	old := time.Now().Add(-60 * 24 * time.Hour)
	issues := append(fixture(),
		bd.Issue{ID: "t-wisp-1", Title: "patrol", IssueType: "molecule", Status: "open", Ephemeral: true, UpdatedAt: old},
		bd.Issue{ID: "t-wisp-2", Parent: "t-wisp-1", Title: "ready wisp step", IssueType: "task", Status: "open", Priority: 1, Ephemeral: true, UpdatedAt: old},
		bd.Issue{ID: "t-wisp-3", Parent: "t-wisp-1", Title: "running wisp step", IssueType: "task", Status: "in_progress", Ephemeral: true, UpdatedAt: old},
	)
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	if a.snap.ByID["t-wisp-2"] != nil || len(a.snap.Ephemeral.Issues) != 3 {
		t.Fatalf("wisps not split out of the snapshot: %d ephemeral", len(a.snap.Ephemeral.Issues))
	}
	if !a.snap.Ephemeral.Ready(a.snap.Ephemeral.ByID["t-wisp-2"]) {
		t.Fatal("the wisp step is ready on its own, so the views below only miss it through the split")
	}
	for _, name := range []string{"Now", "Backlog", "Triage", "Activity", "Board"} {
		v := a.views[a.viewIndex(name)]
		var ids []string
		if k, ok := v.(*kanbanView); ok {
			for _, col := range k.cards {
				for _, is := range col {
					ids = append(ids, is.ID)
				}
			}
		} else {
			for _, r := range rowsOf(v) {
				if r.issue != nil {
					ids = append(ids, r.issue.ID)
				}
			}
		}
		for _, id := range ids {
			if strings.HasPrefix(id, "t-wisp") {
				t.Errorf("%s shows wisp %s", name, id)
			}
		}
	}
	if h := a.snap.Measure(a.snap.Issues, time.Now()); h.Total != len(fixture()) || h.Stale30 != 0 {
		t.Errorf("hygiene counts wisps: total %d stale %d, want %d and 0", h.Total, h.Stale30, len(fixture()))
	}
	for _, r := range a.snap.Triage() {
		for _, is := range r.Issues {
			if is.Ephemeral {
				t.Errorf("triage rule %s lists wisp %s", r.ID, is.ID)
			}
		}
	}
}

func TestFormulaVarsAlignAndScroll(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	a.Update(snapshotMsg{issues: fixture(), gen: a.gen})
	f := &bd.Formula{Name: "big", Description: strings.Repeat("long description ", 80), Vars: map[string]bd.FormulaVar{}}
	for i := range 16 {
		f.Vars[fmt.Sprintf("v%s", strings.Repeat("x", i))] = bd.FormulaVar{Description: strings.Repeat("words ", 25), Required: i == 0, Default: map[bool]string{true: "int"}[i%3 == 1]}
	}
	f.Vars["a"] = bd.FormulaVar{Description: "short", Required: true, Default: "a/default/longer/than/sixteen"}
	f.Steps = []bd.FormulaStep{{ID: "s1", Title: "one"}, {ID: "s2", Title: "two", Needs: []string{"s1"}}}
	v := a.views[a.viewIndex("Workflows")].(*workflowsView)
	v.loaded, v.formulas, v.details["big"] = true, []bd.FormulaSummary{{Name: "big"}}, f
	a.switchTo(a.viewIndex("Workflows"))
	v.Rebuild(a)
	for _, open := range []bool{false, true} {
		if open {
			a.Update(tea.KeyPressMsg{Code: 'v', Text: "v"})
		}
		out := ansi.Strip(a.render())
		if n := len(strings.Split(out, "\n")); n != 30 {
			t.Fatalf("varsOpen=%v: %d lines, want 30", open, n)
		}
		if !open && !strings.Contains(out, "v shows them") {
			t.Fatal("17 vars should collapse so the step graph keeps its room")
		}
		if open {
			col := -1
			for _, l := range strings.Split(out, "\n") {
				if i := strings.Index(l, "words"); i >= 0 && strings.Contains(l, " v") {
					if col >= 0 && i != col {
						t.Fatalf("descriptions start at columns %d and %d", col, i)
					}
					col = i
				}
			}
			if col < 0 {
				t.Fatal("var table not shown")
			}
			for _, want := range []string{"required = a/default", "a/default/longer/than/sixteen)"} {
				if !strings.Contains(out, want) {
					t.Fatalf("var table lacks %q", want)
				}
			}
			sel := v.preview.sel
			a.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
			if v.preview.sel != sel {
				t.Fatal("l moved the hidden step graph")
			}
			before := out
			a.Update(tea.KeyPressMsg{Code: 'J', Text: "J"})
			if ansi.Strip(a.render()) == before {
				t.Fatal("J did not scroll the var table")
			}
		}
	}
}

func TestGraphDownLeavesSingleNodeLayer(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.Update(snapshotMsg{issues: fixture(), gen: a.gen})
	f := bd.Formula{Name: "f", Steps: []bd.FormulaStep{{ID: "plan"}, {ID: "x", Needs: []string{"plan"}}, {ID: "y", Needs: []string{"plan"}}}}
	g := newGraph()
	g.fixed, g.rootID, g.showClosed = f.Snapshot(), "f", true
	g.Rebuild(a)
	if g.sel != "plan" {
		t.Fatalf("start on %s, want plan", g.sel)
	}
	g.Update(a, tea.KeyPressMsg{Code: 'j', Text: "j"})
	if g.sel != "y" {
		t.Fatalf("down from a one-box layer went to %s, want the next row (y)", g.sel)
	}
	g.Update(a, tea.KeyPressMsg{Code: 'k', Text: "k"})
	if g.sel != "x" {
		t.Fatalf("up within the layer went to %s, want x", g.sel)
	}
}
