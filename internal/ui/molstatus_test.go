package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/charmbracelet/x/ansi"
)

// molFixture holds a molecule with a closed step, a step freed by a closed gate, a step behind it
// and a parallel-bonded sub-molecule (a container) whose own step moved last; a second molecule
// with a step in progress; and a wisp, which no persistent molecule may join.
func molFixture() []bd.Issue {
	now := time.Now()
	hour := now.Add(-time.Hour)
	return append(fixture(),
		bd.Issue{ID: "t-mol-1", Title: "release", IssueType: "molecule", Status: "open", UpdatedAt: hour},
		bd.Issue{ID: "t-mol-2", Parent: "t-mol-1", Title: "build", IssueType: "task", Status: "closed", UpdatedAt: hour},
		bd.Issue{ID: "t-mol-3", Parent: "t-mol-1", Title: "sign off", IssueType: "task", Status: "open", UpdatedAt: now.Add(-5 * time.Minute),
			Dependencies: []bd.Dependency{{IssueID: "t-mol-3", DependsOnID: "t-gate-1", Type: "blocks"}}},
		bd.Issue{ID: "t-mol-4", Parent: "t-mol-1", Title: "publish", IssueType: "task", Status: "open", UpdatedAt: hour,
			Dependencies: []bd.Dependency{{IssueID: "t-mol-4", DependsOnID: "t-mol-3", Type: "blocks"}}},
		bd.Issue{ID: "t-mol-5", Parent: "t-mol-1", Title: "extra", IssueType: "molecule", Status: "open", UpdatedAt: hour},
		bd.Issue{ID: "t-mol-6", Parent: "t-mol-5", Title: "extra x", IssueType: "task", Status: "open", UpdatedAt: now.Add(-2 * time.Minute)},
		bd.Issue{ID: "t-gate-1", Title: "Gate: human", IssueType: "gate", Status: "closed", AwaitType: "human", UpdatedAt: hour},
		bd.Issue{ID: "t-mol-9", Title: "docs", IssueType: "molecule", Status: "open", UpdatedAt: hour},
		bd.Issue{ID: "t-mol-10", Parent: "t-mol-9", Title: "write", IssueType: "task", Status: "in_progress", UpdatedAt: now},
		bd.Issue{ID: "t-mol-11", Title: "old run", IssueType: "molecule", Status: "closed", UpdatedAt: hour},
		bd.Issue{ID: "t-wisp-1", Title: "patrol", IssueType: "molecule", Status: "open", Ephemeral: true, UpdatedAt: hour},
	)
}

// gatedFixture is a formula with a gate step, poured (persistent) or as a wisp: the gate is a
// child of the root and the approval step waits on it.
func gatedFixture(prefix string, eph bool) []bd.Issue {
	hour := time.Now().Add(-time.Hour)
	r := prefix + "-1"
	return []bd.Issue{
		{ID: r, Title: "gated", IssueType: "molecule", Status: "open", Ephemeral: eph, UpdatedAt: hour},
		{ID: prefix + "-2", Parent: r, Title: "step a", IssueType: "task", Status: "closed", Ephemeral: eph, UpdatedAt: hour},
		{ID: prefix + "-3", Parent: r, Title: "approval", IssueType: "task", Status: "open", Ephemeral: eph, UpdatedAt: hour,
			Dependencies: []bd.Dependency{{IssueID: prefix + "-3", DependsOnID: prefix + "-g", Type: "blocks"}}},
		{ID: prefix + "-g", Parent: r, Title: "Gate: human", IssueType: "gate", Status: "open", AwaitType: "human", Ephemeral: eph, UpdatedAt: hour},
	}
}

func idList(xs []*bd.Issue) string {
	var out []string
	for _, x := range xs {
		out = append(out, x.ID)
	}
	return strings.Join(out, " ")
}

func TestMoleculeStatusFromSnapshot(t *testing.T) {
	s := bd.NewSnapshot(molFixture())
	st := statusOf(s, s.ByID["t-mol-1"])
	if st.done != 1 || st.total != 4 || len(st.current) != 0 {
		t.Fatalf("progress %d/%d current %v: want the leaf steps at every depth, not the sub-molecule", st.done, st.total, st.current)
	}
	if got := idList(st.ready); got != "t-mol-3 t-mol-6" {
		t.Fatalf("ready %q: want the gate-freed step and the sub-molecule's step, not the sub-molecule", got)
	}
	if got := idList(st.resume); got != "t-mol-3" {
		t.Fatalf("resume %q: want the step behind the closed gate", got)
	}
	if st.lastStep == nil || st.lastStep.ID != "t-mol-6" {
		t.Fatalf("last activity should come from the grandchild that moved last, got %+v", st.lastStep)
	}

	hooked := molFixture()
	for i := range hooked {
		if hooked[i].ID == "t-mol-1" {
			hooked[i].Status = "hooked"
		}
	}
	s = bd.NewSnapshot(hooked)
	if st := statusOf(s, s.ByID["t-mol-1"]); len(st.resume) != 0 {
		t.Fatalf("a hooked root is no resume candidate, as in bd mol ready --gated: %v", st.resume)
	}

	for _, eph := range []bool{false, true} {
		s := bd.NewSnapshot(gatedFixture("t-mol-g", eph))
		src := s
		if eph {
			src = s.Ephemeral
		}
		st := statusOf(src, src.ByID["t-mol-g-1"])
		if st.done != 1 || st.total != 2 || len(st.current)+len(st.ready) != 0 || idList(st.waiting) != "t-mol-g-g" {
			t.Fatalf("ephemeral=%v: a molecule parked on its gate step: %d/%d current %v ready %v waiting %v",
				eph, st.done, st.total, st.current, st.ready, st.waiting)
		}
	}
}

func workflowsApp(t *testing.T, issues []bd.Issue) (*App, *workflowsView) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	a.switchTo(a.viewIndex("Workflows"))
	v := a.views[a.active].(*workflowsView)
	v.Msg(a, molDataMsg{formulas: []bd.FormulaSummary{{Name: "extra", Steps: 1, Vars: 1}}})
	return a, v
}

func pickRow(v *workflowsView, match func(molItem) bool) {
	for i, it := range v.items {
		if it.header == "" && match(it) {
			v.cursor = i
		}
	}
}

func pickIssue(v *workflowsView, id string) {
	pickRow(v, func(it molItem) bool { return it.issue != nil && it.issue.ID == id })
}

func TestMoleculePreview(t *testing.T) {
	a, v := workflowsApp(t, append(molFixture(), gatedFixture("t-mol-g", false)...))
	pickIssue(v, "t-mol-1")
	out := ansi.Strip(a.render())
	for _, want := range []string{"1 to resume", "release 1/4 resume", "progress 1/4 steps · 25%",
		"next     ○ mol-3  sign off", "○ mol-6  extra x", "resume   ○ mol-3  sign off", "activity 2m ago  mol-6  extra x"} {
		if !strings.Contains(out, want) {
			t.Errorf("Workflows view missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "○ mol-5  extra") {
		t.Errorf("the sub-molecule is listed as a step:\n%s", out)
	}
	pickIssue(v, "t-mol-9")
	if out := ansi.Strip(a.render()); !strings.Contains(out, "current  ◐ mol-10  write") || !strings.Contains(out, "activity now  mol-10") {
		t.Errorf("preview should name the in-progress step and say now, not now ago:\n%s", out)
	}
	pickIssue(v, "t-mol-g-1")
	if out := ansi.Strip(a.render()); !strings.Contains(out, "on human gate, holds mol-g-3") {
		t.Errorf("a molecule parked on a gate should say what it waits on:\n%s", out)
	}

	pickIssue(v, "t-mol-1")
	short := ansi.Strip(v.previewPane(a, 100, 3))
	if !strings.Contains(short, "progress") || strings.Contains(short, "activity") {
		t.Errorf("on a short pane the status lines should give way from the bottom:\n%s", short)
	}
}

func TestMoleculeBond(t *testing.T) {
	lastCall := fakeBD(t, `case "$*" in
*--dry-run*) echo; echo "Dry run: bond preview"; echo "  Result: spawn proto, attach to molecule";;
"mol bond"*--json*) printf '{"id_mapping":{"extra":"t-new"}}';;
"formula show"*) printf '{"formula":"extra","vars":{"who":{"required":true}}}';;
esac`)
	a, v := workflowsApp(t, molFixture())
	key := func(k string) tea.Cmd { _, cmd := v.Update(a, tea.KeyPressMsg{Code: rune(k[0]), Text: k}); return cmd }
	guard := func(what string) {
		t.Helper()
		a.modal = nil
		cmd := key("+")
		if a.modal != nil || cmd == nil {
			t.Fatalf("+ on %s should only flash", what)
		}
		if m, ok := cmd().(flashMsg); !ok || !strings.Contains(m.text, "select an open molecule") {
			t.Fatalf("+ on %s flashed %#v", what, m)
		}
	}
	pickRow(v, func(it molItem) bool { return it.formula != nil })
	guard("a formula row")
	v.showClosed = true
	v.Rebuild(a)
	pickIssue(v, "t-mol-11")
	guard("a closed root")
	pickIssue(v, "t-gate-1")
	guard("a gate")
	v.showClosed = false
	v.Rebuild(a)

	pickIssue(v, "t-mol-1")
	key("+")
	var opts []string
	for _, o := range a.modal.options {
		opts = append(opts, o.value)
	}
	if got := strings.Join(opts, " "); got != "f:extra m:t-mol-9" {
		t.Fatalf("bond picker options %q: want the formula and the other open persistent molecule, no wisp", got)
	}
	a.modal.onSubmit([]string{"m:t-mol-9"})
	dry := a.modal.onSubmit([]string{"parallel"})()
	if got := lastCall(); got != "mol bond t-mol-1 t-mol-9 --type parallel --dry-run" {
		t.Fatalf("join dry run ran bd %q", got)
	}
	v.Msg(a, dry)
	if !strings.Contains(a.modal.title, "Dry run: bond preview") {
		t.Fatalf("the confirm should show bd's dry run: %q", a.modal.title)
	}
	if m := a.modal.onSubmit(nil)().(writeDoneMsg); m.err != nil || lastCall() != "mol bond t-mol-1 t-mol-9 --type parallel --json" {
		t.Fatalf("join ran bd %q: %+v", lastCall(), m)
	}

	v.details = map[string]*bd.Formula{}
	key("+")
	a.modal.onSubmit([]string{"f:extra"})
	fetched := a.modal.onSubmit([]string{"sequential"})().(bondFormulaMsg)
	if fetched.err != nil || lastCall() != "formula show extra --json" {
		t.Fatalf("an unloaded formula should be read on demand: bd %q, %+v", lastCall(), fetched)
	}
	if m, ok := v.Msg(a, bondFormulaMsg{b: fetched.b, err: errors.New("no such formula")})().(flashMsg); !ok || !m.err {
		t.Fatalf("a failed formula read should flash an error, got %#v", m)
	}
	v.Msg(a, fetched)
	dry = a.modal.onSubmit([]string{"ace"})()
	if got := lastCall(); got != "mol bond t-mol-1 extra --type sequential --var who=ace --dry-run" {
		t.Fatalf("formula dry run ran bd %q", got)
	}
	v.Msg(a, dry)
	m := a.modal.onSubmit(nil)().(writeDoneMsg)
	if m.err != nil || !strings.Contains(m.desc, "as t-new") || lastCall() != "update t-new --add-label machine:u-host" {
		t.Fatalf("a sequential formula bond should label its new root for this host: bd %q, %+v", lastCall(), m)
	}
}
