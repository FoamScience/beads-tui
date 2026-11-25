package bd

import "testing"

func TestWispBlockersCloseAndChain(t *testing.T) {
	s := NewSnapshot([]Issue{
		{ID: "t-1", Title: "waits on a closed wisp", IssueType: "task", Status: "open",
			Dependencies: []Dependency{{IssueID: "t-1", DependsOnID: "t-wisp-2", Type: "blocks"}}},
		{ID: "t-2", Title: "chain head", IssueType: "task", Status: "open",
			Dependencies: []Dependency{{IssueID: "t-2", DependsOnID: "t-wisp-3", Type: "blocks"}}},
		{ID: "t-3", Title: "chain tail", IssueType: "task", Status: "open"},
		{ID: "t-4", Parent: "t-wisp-1", Title: "regular child of a blocked wisp", IssueType: "task", Status: "open"},
		{ID: "t-wisp-1", Title: "patrol", IssueType: "molecule", Status: "open", Ephemeral: true,
			Dependencies: []Dependency{{IssueID: "t-wisp-1", DependsOnID: "t-3", Type: "blocks"}}},
		{ID: "t-wisp-2", Parent: "t-wisp-1", Title: "done step", IssueType: "task", Status: "closed", Ephemeral: true},
		{ID: "t-wisp-3", Parent: "t-wisp-1", Title: "open step", IssueType: "task", Status: "open", Ephemeral: true},
	})
	if !s.Ready(s.ByID["t-1"]) {
		t.Fatal("a closed wisp blocker should release the bead")
	}
	chain := s.BlockChain(s.ByID["t-2"])
	if len(chain) != 2 || chain[0].ID != "t-wisp-3" || chain[1].ID != "t-3" {
		t.Fatalf("block chain through a wisp = %v, want t-wisp-3 then t-3 (via its blocked root)", ids(chain))
	}
	if s.Ready(s.ByID["t-4"]) {
		t.Fatal("an ancestor walk through a blocked wisp root must block the child")
	}
}

func ids(is []*Issue) []string {
	out := make([]string, len(is))
	for i, x := range is {
		out[i] = x.ID
	}
	return out
}
