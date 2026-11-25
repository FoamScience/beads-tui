package ui

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/FoamScience/beads-tui/internal/dispatch"
	"github.com/charmbracelet/x/ansi"
)

// TestWispActions drives Q, B and X on a wisp root row against a fake bd that logs its args.
func TestWispActions(t *testing.T) {
	lastCall := fakeBD(t, `case "$*" in *"gc --dry-run"*) printf '{"cleaned_ids":["t-wisp-1"],"issue_ids":["t-wisp-gone","t-wisp-1"]}';; esac`)
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	now := time.Now()
	issues := append(fixture(),
		bd.Issue{ID: "t-wisp-1", Title: "patrol", IssueType: "molecule", Status: "open", Ephemeral: true, UpdatedAt: now},
		bd.Issue{ID: "t-wisp-2", Parent: "t-wisp-1", Title: "wisp step", IssueType: "task", Status: "open", Ephemeral: true, UpdatedAt: now},
	)
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	a.switchTo(a.viewIndex("Workflows"))
	v := a.views[a.active].(*workflowsView)
	v.Msg(a, molDataMsg{})
	for i, it := range v.items {
		if it.issue != nil && it.issue.ID == "t-wisp-1" {
			v.cursor = i
		}
	}
	key := func(k string) { v.Update(a, tea.KeyPressMsg{Code: rune(k[0]), Text: k}) }

	key("Q")
	a.modal.onSubmit(nil)()
	if got := lastCall(); got != "mol squash t-wisp-1" {
		t.Fatalf("Q on a wisp root ran bd %q", got)
	}
	key("B")
	a.modal.onSubmit(nil)()
	if got := lastCall(); got != "mol burn t-wisp-1 --force" {
		t.Fatalf("B on a wisp root ran bd %q", got)
	}

	key("X")
	if cmd := a.modal.onSubmit([]string{"foo"}); cmd != nil {
		t.Fatal("typed text that matches no gc mode must not start a preview")
	}
	for mode, want := range map[string]string{"abandoned": "mol wisp gc --dry-run --json", "closed": "mol wisp gc --dry-run --closed --json"} {
		m := a.modal.onSubmit([]string{mode})().(wispGCMsg)
		if got := lastCall(); got != want || m.mode != mode || m.err != nil {
			t.Fatalf("%s preview ran bd %q (want %q), msg %+v", mode, got, want, m)
		}
		if got := strings.Join(m.ids, " "); got != "t-wisp-1 t-wisp-gone" {
			t.Fatalf("%s preview ids %s, want each id once", mode, got)
		}
	}

	cmd := v.Msg(a, wispGCMsg{mode: "closed"})
	if m, ok := cmd().(flashMsg); !ok || !strings.Contains(m.text, "nothing to collect") {
		t.Fatalf("empty dry run flash = %#v", m)
	}
	v.Msg(a, wispGCMsg{mode: "closed", ids: []string{"t-wisp-1", "t-wisp-gone"}})
	if !strings.Contains(a.modal.title, "Delete 2 closed wisp rows on every machine") ||
		!strings.Contains(a.modal.title, "patrol") || !strings.Contains(a.modal.title, "wisp-gone") {
		t.Fatalf("gc confirm should count and list the dry run's ids: %q", a.modal.title)
	}
	a.modal.onSubmit(nil)()
	if got := lastCall(); got != "delete --force t-wisp-1 t-wisp-gone" {
		t.Fatalf("gc confirm ran bd %q, want a delete of exactly the previewed ids", got)
	}
}

func TestMoleculeStaleCountsDirectChildrenOnly(t *testing.T) {
	s := bd.NewSnapshot([]bd.Issue{
		{ID: "t-mol-1", Title: "pour", IssueType: "molecule", Status: "open"},
		{ID: "t-mol-2", Parent: "t-mol-1", Title: "step", IssueType: "task", Status: "closed"},
		{ID: "t-mol-3", Parent: "t-mol-2", Title: "sub-step", IssueType: "task", Status: "open"},
	})
	if !stale(s, s.ByID["t-mol-1"]) {
		t.Fatal("closed direct child with an open grandchild is stale, as bd mol stale counts direct children")
	}
}

func TestCachedSnapshotShowsWispsAsLoading(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	now := time.Now()
	issues := append(fixture(),
		bd.Issue{ID: "t-wisp-1", Title: "patrol", IssueType: "molecule", Status: "open", Ephemeral: true, UpdatedAt: now},
	)
	saveCache(dir, issues)
	if got := loadCache(dir); len(got) != len(fixture()) || slices.ContainsFunc(got, func(i bd.Issue) bool { return i.Ephemeral }) {
		t.Fatalf("cache kept ephemeral rows: %d rows", len(got))
	}
	a := New(bd.Client{Dir: dir})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	a.switchTo(a.viewIndex("Workflows"))
	a.views[a.active].(*workflowsView).Msg(a, molDataMsg{})
	if out := ansi.Strip(a.render()); !strings.Contains(out, "wisps loading…") || strings.Contains(out, "0 wisps") {
		t.Fatalf("cached draw should not claim 0 wisps:\n%s", out)
	}
	a.Update(snapshotMsg{err: errors.New("dolt locked"), gen: a.gen})
	if out := ansi.Strip(a.render()); !strings.Contains(out, "wisps not loaded") || strings.Contains(out, "wisps loading…") {
		t.Fatalf("a failed first load should not keep claiming the wisps are loading:\n%s", out)
	}
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	if out := ansi.Strip(a.render()); !strings.Contains(out, "· 1 wisp") || strings.Contains(out, "1 wisps") {
		t.Fatalf("wisp count missing after the bd load:\n%s", out)
	}
}

func TestWispDetailReadsTheWispSide(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	issues := func() []bd.Issue {
		now := time.Now()
		return append(fixture(),
			bd.Issue{ID: "t-r", Title: "regular blocker", IssueType: "task", Status: "open", UpdatedAt: now},
			bd.Issue{ID: "t-wisp-1", Title: "patrol", IssueType: "molecule", Status: "open", Ephemeral: true, UpdatedAt: now},
			bd.Issue{ID: "t-wisp-2", Parent: "t-wisp-1", Title: "first step", IssueType: "task", Status: "open", Ephemeral: true, UpdatedAt: now,
				Dependencies: []bd.Dependency{{IssueID: "t-wisp-2", DependsOnID: "t-r", Type: "blocks"}}},
			bd.Issue{ID: "t-wisp-3", Parent: "t-wisp-1", Title: "second step", IssueType: "task", Status: "open", Ephemeral: true, UpdatedAt: now,
				Dependencies: []bd.Dependency{{IssueID: "t-wisp-3", DependsOnID: "t-wisp-2", Type: "blocks"}}},
		)
	}
	a.Update(snapshotMsg{issues: issues(), gen: a.gen})
	a.switchTo(a.viewIndex("Workflows"))
	a.views[a.active].(*workflowsView).Msg(a, molDataMsg{})
	key := func(k tea.KeyPressMsg) string { a.Update(k); return ansi.Strip(a.render()) }
	for i := 0; i < 20 && (a.selected() == nil || a.selected().ID != "t-wisp-1"); i++ {
		key(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	if a.selected() == nil || a.selected().ID != "t-wisp-1" {
		t.Fatalf("no Workflows row for the wisp root, selected %+v", a.selected())
	}
	if out := key(tea.KeyPressMsg{Code: tea.KeyEnter}); !a.detailOpen || !strings.Contains(out, "first step") || !strings.Contains(out, "second step") {
		t.Fatalf("Enter on the wisp row should open its detail with its steps:\n%s", out)
	}
	follow := func(id string) string {
		t.Helper()
		for i := 0; i < 10 && a.detail.selectedLink() != id; i++ {
			key(tea.KeyPressMsg{Code: tea.KeyTab})
		}
		if a.detail.selectedLink() != id {
			t.Fatalf("no detail link to %s in %v", id, a.detail.links)
		}
		out := key(tea.KeyPressMsg{Code: tea.KeyEnter})
		if a.override == nil || a.override.ID != id {
			t.Fatalf("enter on link %s opened %+v", id, a.override)
		}
		return out
	}
	out := follow("t-wisp-2")
	for _, want := range []string{"(blocked)", "parent", "patrol", "regular blocker", "needed by", "second step"} {
		if !strings.Contains(out, want) {
			t.Errorf("wisp step detail missing %q:\n%s", want, out)
		}
	}
	a.Update(snapshotMsg{issues: issues(), gen: a.gen})
	if a.override == nil || a.override.ID != "t-wisp-2" || a.override != a.snap.Ephemeral.ByID["t-wisp-2"] {
		t.Fatalf("a reload should keep the wisp open in the detail, got %+v", a.override)
	}
	if out := follow("t-r"); !strings.Contains(out, "needed by") || !strings.Contains(out, "first step") {
		t.Fatalf("a regular bead's detail should list the wisp step waiting on it:\n%s", out)
	}
}

func TestBeadBlockedByWispStaysOutOfReadyViews(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.state.Machine = allMachines
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	now := time.Now()
	issues := append(fixture(),
		bd.Issue{ID: "t-q", Title: "waits on a wisp", IssueType: "task", Status: "open", UpdatedAt: now,
			Dependencies: []bd.Dependency{{IssueID: "t-q", DependsOnID: "t-wisp-2", Type: "blocks"}}},
		bd.Issue{ID: "t-wisp-1", Title: "patrol", IssueType: "molecule", Status: "open", Ephemeral: true, UpdatedAt: now},
		bd.Issue{ID: "t-wisp-2", Parent: "t-wisp-1", Title: "open wisp step", IssueType: "task", Status: "open", Ephemeral: true, UpdatedAt: now},
	)
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	if st := dispatch.StateOf(a.snap, a.snap.ByID["t-q"]); st != dispatch.Waiting {
		t.Errorf("dispatch state of a bead blocked by a wisp = %s, want %s", st, dispatch.Waiting)
	}
	ids := func(name string) []string {
		var out []string
		if k, ok := a.views[a.viewIndex(name)].(*kanbanView); ok {
			for _, col := range k.cards {
				for _, is := range col {
					out = append(out, is.ID)
				}
			}
			return out
		}
		for _, r := range rowsOf(a.views[a.viewIndex(name)]) {
			if r.issue != nil {
				out = append(out, r.issue.ID)
			}
		}
		return out
	}
	backlog := ids("Backlog")
	if slices.Contains(backlog, "t-q") || !slices.Contains(backlog, "t-a.3") {
		t.Errorf("Backlog = %v: want the ready fixture child, not the bead waiting on a wisp", backlog)
	}
	for _, name := range []string{"Backlog", "Board", "Triage"} {
		for _, id := range ids(name) {
			if strings.HasPrefix(id, "t-wisp") {
				t.Errorf("%s shows wisp %s", name, id)
			}
		}
	}
}
