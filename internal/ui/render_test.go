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
	for _, size := range [][2]int{{60, 15}, {80, 24}, {120, 40}, {160, 50}} {
		for tab := range 10 {
			for _, enter := range []bool{false, true} {
				name := fmt.Sprintf("%dx%d/tab%d/enter=%v", size[0], size[1], tab+1, enter)
				a := New(bd.Client{Dir: t.TempDir()})
				a.state, a.host = defaultState(), "u-host"
				a.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				a.Update(snapshotMsg{issues: fixture()})
				a.active = tab
				if tab == 5 {
					a.views[5].(*graphView).setRoot(a, a.snap.ByID["t-a.1"])
				}
				if enter {
					a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				}
				out := a.render()
				if head := ansi.Strip(strings.SplitN(out, "\n", 2)[0]); !strings.Contains(head, "1 ") || !strings.Contains(head, fmt.Sprint(len(a.views)%10)) {
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
	if !ready(s, s.ByID["t-a.3"]) || ready(s, s.ByID["t-a.2"]) || ready(s, s.ByID["t-a"]) {
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
	if out := ansi.Strip(a.render()); !strings.Contains(out, "▶ tm-tests") {
		t.Fatalf("Now row lacks the live agent:\n%s", out)
	}
	a.Update(herdrMsg{panes: map[string]agentPane{}})
	a.Update(sessionNamesMsg{"aaaa-bbbb": {Name: "8010"}})
	if out := ansi.Strip(a.render()); !strings.Contains(out, "○ ended 8010") {
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
	for _, name := range []string{"Now", "Ready", "Epics", "Triage", "Activity", "Inbox", "Board"} {
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

func rowsOf(v View) []row {
	switch x := v.(type) {
	case *nowView:
		return x.rows
	case *readyView:
		return x.rows
	case *epicsView:
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
