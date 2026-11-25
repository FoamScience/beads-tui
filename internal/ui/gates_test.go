package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/charmbracelet/x/ansi"
)

func gateFixture() []bd.Issue {
	now := time.Now()
	waiter := func(id, gate, machine string) bd.Issue {
		is := bd.Issue{ID: id, Title: "waits on " + gate, IssueType: "task", Status: "open", UpdatedAt: now,
			Dependencies: []bd.Dependency{{IssueID: id, DependsOnID: gate, Type: "blocks"}}}
		if machine != "" {
			is.Labels = []string{"machine:" + machine}
		}
		return is
	}
	gate := func(id, status string) bd.Issue {
		return bd.Issue{ID: id, Title: "Gate " + id, IssueType: "gate", Status: status, AwaitType: "human", UpdatedAt: now}
	}
	return append(fixture(),
		gate("t-gate", "open"), waiter("t-w", "t-gate", "u-host"),
		gate("t-gate-far", "open"), waiter("t-w-far", "t-gate-far", "other-host"),
		gate("t-gate-lone", "open"),
		gate("t-gate-done", "closed"), waiter("t-w-done", "t-gate-done", "u-host"),
	)
}

func gateApp(t *testing.T, issues []bd.Issue) (*App, *workflowsView) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	a.switchTo(a.viewIndex("Workflows"))
	v := a.views[a.active].(*workflowsView)
	v.Msg(a, molDataMsg{})
	return a, v
}

func listedGates(v *workflowsView) []string {
	var out []string
	for _, it := range v.items {
		if isGate(&it) {
			out = append(out, it.issue.ID)
		}
	}
	slices.Sort(out)
	return out
}

func selectItem(t *testing.T, v *workflowsView, id string) {
	t.Helper()
	for i, it := range v.items {
		if it.issue != nil && it.issue.ID == id {
			v.cursor = i
			return
		}
	}
	t.Fatalf("%s not listed in Workflows", id)
}

func gateKey(a *App, v *workflowsView, k string) tea.Cmd {
	_, cmd := v.Update(a, tea.KeyPressMsg{Code: rune(k[0]), Text: k})
	return cmd
}

// TestGateListing: gates stay out of work views, block their waiters, and Workflows lists them
// by the machine of their waiters; a gate with no known waiter shows on every machine.
func TestGateListing(t *testing.T) {
	a, v := gateApp(t, gateFixture())
	for _, id := range []string{"t-gate", "t-gate-far", "t-gate-lone", "t-gate-done"} {
		if a.snap.ByID[id] != nil || a.snap.Gates.ByID[id] == nil {
			t.Fatalf("%s must sit only in the gate index", id)
		}
	}
	if b := a.snap.Blockers(a.snap.ByID["t-w"]); len(b) != 1 || b[0].ID != "t-gate" {
		t.Fatalf("blockers of t-w = %v, want t-gate", b)
	}
	if a.snap.IsBlocked(a.snap.ByID["t-w-done"]) {
		t.Fatal("a closed gate must not block")
	}

	for _, name := range []string{"Backlog", "Now", "Triage", "Inbox"} {
		a.switchTo(a.viewIndex(name))
		for _, r := range rowsOf(a.views[a.active]) {
			if r.issue != nil && r.issue.IssueType == "gate" {
				t.Errorf("%s lists gate %s", name, r.issue.ID)
			}
		}
	}
	a.state.Machine = allMachines
	a.switchTo(a.viewIndex("Board"))
	for _, col := range a.views[a.active].(*kanbanView).cards {
		for _, is := range col {
			if is.IssueType == "gate" {
				t.Errorf("Board shows gate %s", is.ID)
			}
		}
	}
	a.state.Machine = ""
	if h := a.snap.Measure(a.snap.Issues, time.Now()); h.Total != len(gateFixture())-4 {
		t.Errorf("hygiene total %d counts gates, want %d", h.Total, len(gateFixture())-4)
	}

	a.switchTo(a.viewIndex("Workflows"))
	v.Rebuild(a)
	if got := strings.Join(listedGates(v), " "); got != "t-gate t-gate-lone" {
		t.Fatalf("gates on u-host = %q, want the one whose waiter is here plus the waiter-less one", got)
	}
	gateKey(a, v, "H")
	if got := strings.Join(listedGates(v), " "); got != "t-gate t-gate-done t-gate-lone" {
		t.Fatalf("H should add the closed gate, got %q", got)
	}
	a.state.Machine = allMachines
	v.Rebuild(a)
	if got := len(listedGates(v)); got != 4 {
		t.Fatalf("all machines lists %d gates, want 4", got)
	}
}

func TestGateResolve(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "list.json")
	lastCall := fakeBD(t, fmt.Sprintf(`case "$1" in list) cat %s;; esac`, list))
	a, v := gateApp(t, gateFixture())

	gateKey(a, v, "H")
	selectItem(t, v, "t-gate-done")
	gateKey(a, v, "G")
	if a.modal != nil {
		t.Fatal("G on a closed gate must not prompt")
	}

	selectItem(t, v, "t-gate")
	gateKey(a, v, "G")
	if cmd := a.modal.onSubmit([]string{""}); cmd != nil {
		t.Fatal("the reason prompt must lead to a confirm, not a write")
	}
	if !strings.Contains(a.modal.title, "unblocks") || !strings.Contains(a.modal.title, "waits on t-gate") {
		t.Fatalf("resolve confirm should name the waiters it unblocks: %q", a.modal.title)
	}
	done := a.modal.onSubmit(nil)()
	if got := lastCall(); got != "gate resolve t-gate" {
		t.Fatalf("G with an empty reason ran bd %q", got)
	}
	if !a.snap.Gates.ByID["t-gate"].Closed() || a.snap.IsBlocked(a.snap.ByID["t-w"]) {
		t.Fatal("resolve must close the gate and unblock its waiter at once")
	}

	resolved := gateFixture()
	for i := range resolved {
		if resolved[i].ID == "t-gate" {
			resolved[i].Status = "closed"
		}
	}
	b, _ := json.Marshal(resolved)
	if err := os.WriteFile(list, b, 0o644); err != nil {
		t.Fatal(err)
	}
	_, reload := a.Update(done)
	a.Update(reload())
	if got := lastCall(); !strings.HasPrefix(got, "list ") || !strings.Contains(got, "--include-gates") {
		t.Fatalf("the reload after resolve ran bd %q", got)
	}
	if g := a.snap.Gates.ByID["t-gate"]; g == nil || !g.Closed() || a.snap.IsBlocked(a.snap.ByID["t-w"]) {
		t.Fatal("after the reload the gate is closed and the waiter free")
	}

	selectItem(t, v, "t-gate-lone")
	gateKey(a, v, "G")
	a.modal.onSubmit([]string{" approved "})
	a.modal.onSubmit(nil)()
	if got := lastCall(); got != "gate resolve t-gate-lone --reason approved" {
		t.Fatalf("G with a reason ran bd %q", got)
	}
}

func TestGateCheck(t *testing.T) {
	lastCall := fakeBD(t, "")
	a, v := gateApp(t, gateFixture())
	v.cursor = 1
	gateKey(a, v, "T")
	if a.modal != nil {
		t.Fatal("T off a gate row must not offer a check")
	}
	selectItem(t, v, "t-gate")
	gateKey(a, v, "T")
	if a.modal == nil || !strings.Contains(a.modal.title, "3 open gates") {
		t.Fatalf("T should confirm and count the open gates first, got %+v", a.modal)
	}
	a.modal.onSubmit(nil)()
	if got := lastCall(); got != "gate check" {
		t.Fatalf("T ran bd %q", got)
	}
}

// TestGatePreviewFits keeps the action hint visible when the waiter list is longer than the pane.
func TestGatePreviewFits(t *testing.T) {
	issues := gateFixture()
	for i := range 30 {
		id := fmt.Sprintf("t-x%d", i)
		issues = append(issues, bd.Issue{ID: id, Title: "extra waiter", IssueType: "task", Status: "open",
			Dependencies: []bd.Dependency{{IssueID: id, DependsOnID: "t-gate", Type: "waits-for"}}})
	}
	a, v := gateApp(t, issues)
	selectItem(t, v, "t-gate")
	for _, h := range []int{8, 14, 25} {
		out := ansi.Strip(gatePreview(a, a.snap.Gates.ByID["t-gate"], 60, h))
		lines := strings.Split(out, "\n")
		if len(lines) > h || !strings.Contains(lines[len(lines)-1], "G resolve") {
			t.Fatalf("h=%d: %d lines, last %q", h, len(lines), lines[len(lines)-1])
		}
		if h >= 14 && !strings.Contains(out, "more") {
			t.Fatalf("h=%d: the cut waiter list should say how many are left:\n%s", h, out)
		}
	}
	out := ansi.Strip(a.View().Content)
	for _, want := range []string{"Gates", "Blocks (31)", "G resolve"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Workflows view lacks %q:\n%s", want, out)
		}
	}
}

func TestCacheKeepsEveryGate(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	saveCache(dir, []bd.Issue{
		{ID: "t-g", IssueType: "gate", Status: "open"},
		{ID: "t-wisp-g", IssueType: "gate", Status: "open", Ephemeral: true},
		{ID: "t-wisp-1", IssueType: "task", Status: "open", Ephemeral: true},
	})
	var ids []string
	for _, is := range loadCache(dir) {
		ids = append(ids, is.ID)
	}
	if strings.Join(ids, " ") != "t-g t-wisp-g" {
		t.Fatalf("cache kept %v, want both gates and no wisp", ids)
	}
}
