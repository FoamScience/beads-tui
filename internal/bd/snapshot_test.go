package bd

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBlockersCrossTheEphemeralSplit(t *testing.T) {
	s := NewSnapshot([]Issue{
		{ID: "t-1", Title: "waits on a wisp", IssueType: "task", Status: "open",
			Dependencies: []Dependency{{IssueID: "t-1", DependsOnID: "t-wisp-2", Type: "blocks"}}},
		{ID: "t-2", Title: "blocks a wisp step", IssueType: "task", Status: "open"},
		{ID: "t-wisp-1", Title: "patrol", IssueType: "molecule", Status: "open", Ephemeral: true},
		{ID: "t-wisp-2", Parent: "t-wisp-1", Title: "wisp step", IssueType: "task", Status: "open", Ephemeral: true},
		{ID: "t-wisp-3", Parent: "t-wisp-1", Title: "waiting wisp step", IssueType: "task", Status: "open", Ephemeral: true,
			Dependencies: []Dependency{{IssueID: "t-wisp-3", DependsOnID: "t-2", Type: "blocks"}}},
	})
	e := s.Ephemeral
	if s.ByID["t-wisp-2"] != nil || e.ByID["t-2"] != nil {
		t.Fatal("rows leaked across the split")
	}
	if s.Ready(s.ByID["t-1"]) || len(s.BlockChain(s.ByID["t-1"])) != 1 {
		t.Fatal("a bead waiting on an open wisp step must stay blocked")
	}
	if e.Ready(e.ByID["t-wisp-3"]) || !e.IsBlocked(e.ByID["t-wisp-3"]) {
		t.Fatal("a wisp step waiting on an open regular bead must show blocked")
	}
	if !e.Ready(e.ByID["t-wisp-2"]) {
		t.Fatal("the unblocked wisp step should be ready")
	}
	if d := s.Dependents("t-wisp-2"); len(d) != 1 || d[0].IssueID != "t-1" {
		t.Fatalf("dependents of a wisp step = %v, want t-1", d)
	}
	if d := s.Dependents("t-2"); len(d) != 1 || d[0].IssueID != "t-wisp-3" {
		t.Fatalf("dependents of t-2 = %v, want t-wisp-3", d)
	}
	if s.Find("t-wisp-2") == nil || e.Find("t-2") == nil || s.Of(e.ByID["t-wisp-2"]) != e || e.Of(s.ByID["t-2"]) != s {
		t.Fatal("Find/Of should resolve ids on either side")
	}
}

func TestGatesBlockAcrossTheSplit(t *testing.T) {
	s := NewSnapshot([]Issue{
		{ID: "t-1", Title: "waiter", IssueType: "task", Status: "open",
			Dependencies: []Dependency{{IssueID: "t-1", DependsOnID: "t-g1", Type: "blocks"}}},
		{ID: "t-wisp-1", Title: "wisp waiter", IssueType: "task", Status: "open", Ephemeral: true,
			Dependencies: []Dependency{{IssueID: "t-wisp-1", DependsOnID: "t-g2", Type: "blocks"}}},
		{ID: "t-g1", Title: "Gate: human", IssueType: "gate", Status: "open", AwaitType: "human"},
		{ID: "t-g2", Title: "Gate: timer", IssueType: "gate", Status: "open", AwaitType: "timer", Ephemeral: true},
	})
	if s.ByID["t-g1"] != nil || s.Ephemeral.ByID["t-g2"] != nil || len(s.Gates.Issues) != 2 {
		t.Fatal("gates must sit in their own index, ephemeral or not")
	}
	if b := s.Blockers(s.ByID["t-1"]); len(b) != 1 || b[0].ID != "t-g1" || s.Ready(s.ByID["t-1"]) {
		t.Fatal("an open gate must block its waiter")
	}
	if !s.Ephemeral.IsBlocked(s.Ephemeral.ByID["t-wisp-1"]) {
		t.Fatal("a gate must block a wisp waiter too")
	}
	if d := s.Gates.Dependents("t-g1"); len(d) != 1 || d[0].IssueID != "t-1" {
		t.Fatalf("dependents of a gate = %v, want t-1", d)
	}
	if s.Of(s.Gates.ByID["t-g2"]) != s.Gates || s.Gates.Find("t-wisp-1") == nil {
		t.Fatal("Find/Of should resolve gates and reach out from them")
	}
}

func TestGateStepsStayChildren(t *testing.T) {
	s := NewSnapshot([]Issue{
		{ID: "t-mol-1", Title: "mol", IssueType: "molecule", Status: "open"},
		{ID: "t-mol-2", Parent: "t-mol-1", Title: "step", IssueType: "task", Status: "closed"},
		{ID: "t-mol-3", Parent: "t-mol-1", Title: "Gate: timer", IssueType: "gate", Status: "open"},
		{ID: "t-wisp-1", Title: "wisp", IssueType: "molecule", Status: "open", Ephemeral: true},
		{ID: "t-wisp-2", Parent: "t-wisp-1", Title: "Gate: human", IssueType: "gate", Status: "open", Ephemeral: true},
	})
	if k := s.Children["t-mol-1"]; len(k) != 2 || k[1].ID != "t-mol-3" {
		t.Fatalf("molecule children = %v, want the step and the gate", k)
	}
	if done, total := s.Progress("t-mol-1"); done != 1 || total != 2 {
		t.Fatalf("progress = %d/%d, want 1/2 with the open gate step", done, total)
	}
	if k := s.Ephemeral.Children["t-wisp-1"]; len(k) != 1 || k[0].ID != "t-wisp-2" {
		t.Fatalf("wisp children = %v, want its gate", k)
	}
	if s.ByID["t-mol-3"] != nil || s.Ready(s.Gates.ByID["t-mol-3"]) {
		t.Fatal("a gate step is no work item: not in the main index, never ready")
	}
}

func TestGateEdgesAndResolution(t *testing.T) {
	waiter := func(id, typ string) Issue {
		return Issue{ID: id, IssueType: "task", Status: "open",
			Dependencies: []Dependency{{IssueID: id, DependsOnID: "t-g", Type: typ}}}
	}
	for _, closed := range []bool{false, true} {
		g := Issue{ID: "t-g", IssueType: "gate", Status: "open"}
		if closed {
			g.Status = "closed"
		}
		s := NewSnapshot([]Issue{g, waiter("t-1", "blocks"), waiter("t-2", "waits-for"), waiter("t-3", "conditional-blocks"), waiter("t-4", "related")})
		for _, id := range []string{"t-1", "t-2", "t-3"} {
			if s.IsBlocked(s.ByID[id]) == closed {
				t.Fatalf("%s via a %s gate: blocked=%v", id, g.Status, !closed)
			}
		}
		if s.IsBlocked(s.ByID["t-4"]) {
			t.Fatal("a related edge to a gate must not block")
		}
	}
}

// TestGateDecode reads a real bd 1.3.4 `bd list --include-gates --json` payload (scratch DB):
// timeout in integer nanoseconds or absent, waiters as notify addresses.
func TestGateDecode(t *testing.T) {
	payload := `[{"id":"g-mol-6ny","title":"Gate: timer","description":"Async gate for step b","status":"open","priority":2,"issue_type":"gate","created_at":"2026-11-25T14:26:34Z","updated_at":"2026-11-25T14:26:34Z","dependencies":[{"issue_id":"g-mol-6ny","depends_on_id":"g-mol-z8x","type":"parent-child","created_at":"2026-11-25T14:26:34Z","created_by":"Elwardi","metadata":"{}"}],"await_type":"timer","timeout":1800000000000,"dependency_count":0,"dependent_count":1,"comment_count":0,"parent":"g-mol-z8x"},
{"id":"g-36o","title":"Gate: human","description":"Ad-hoc gate blocking g-1dg\n\nReason: need review","status":"open","priority":2,"issue_type":"gate","created_at":"2026-11-25T14:19:16Z","updated_at":"2026-11-25T14:19:47Z","await_type":"human","waiters":["g-nxa"],"dependency_count":0,"dependent_count":1,"comment_count":0},
{"id":"g-odd","issue_type":"gate","status":"open","timeout":"45m"},
{"id":"g-junk","issue_type":"gate","status":"open","timeout":{"seconds":3}}]`
	var is []Issue
	if err := json.Unmarshal([]byte(payload), &is); err != nil {
		t.Fatalf("one odd gate failed the whole list: %v", err)
	}
	want := []time.Duration{30 * time.Minute, 0, 45 * time.Minute, 0}
	for i, w := range want {
		if got := is[i].Timeout.Duration(); got != w {
			t.Fatalf("%s timeout = %v, want %v", is[i].ID, got, w)
		}
	}
	if is[0].AwaitType != "timer" || is[0].Parent != "g-mol-z8x" || len(is[1].Waiters) != 1 || is[1].Waiters[0] != "g-nxa" {
		t.Fatalf("decoded %+v", is[:2])
	}
}
