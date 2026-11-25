package bd

import "testing"

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
