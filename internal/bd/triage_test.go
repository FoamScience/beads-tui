package bd

import (
	"testing"
	"time"
)

func TestTriage(t *testing.T) {
	old := time.Now().Add(-100 * time.Hour)
	s := NewSnapshot([]Issue{
		{ID: "x-e", IssueType: "epic", Status: "open", Labels: []string{"machine:h"}, ExternalRef: "K-1", EstimatedMinutes: 60},
		{ID: "x-e.1", Parent: "x-e", IssueType: "task", Status: "in_progress", UpdatedAt: old, Labels: []string{"repo:r"}},
		{ID: "x-loose", IssueType: "task", Status: "open", Labels: []string{"machine:h"}},
		{ID: "x-done", IssueType: "task", Status: "closed"},
	})
	want := map[string][]string{
		"no-machine-label":          {"x-e.1"},
		"root-without-external-ref": {"x-loose"},
		"root-without-estimate":     {"x-loose"},
		"not-under-epic":            {"x-loose"},
		"stale-in-progress":         {"x-e.1"},
		"repo-task-without-refs":    {"x-e.1"},
		"closable-epic":             nil,
	}
	for _, r := range s.Triage() {
		var got []string
		for _, is := range r.Issues {
			got = append(got, is.ID)
		}
		if len(got) != len(want[r.ID]) || (len(got) > 0 && got[0] != want[r.ID][0]) {
			t.Errorf("%s: got %v want %v", r.ID, got, want[r.ID])
		}
	}
}

func TestBlockChainReachesTheActionableBlocker(t *testing.T) {
	s := NewSnapshot([]Issue{
		{ID: "x-a", Status: "open", Dependencies: []Dependency{{IssueID: "x-a", DependsOnID: "x-b", Type: "blocks"}}},
		{ID: "x-b", Status: "open", Dependencies: []Dependency{{IssueID: "x-b", DependsOnID: "x-c", Type: "blocks"}}},
		{ID: "x-c", Status: "in_progress"},
		{ID: "x-p", Status: "open", IssueType: "epic", Dependencies: []Dependency{{IssueID: "x-p", DependsOnID: "x-c", Type: "blocks"}}},
		{ID: "x-p.1", Parent: "x-p", Status: "open"},
		{ID: "x-e", IssueType: "epic", Status: "open"},
		{ID: "x-e.1", Parent: "x-e", Status: "closed"},
	})
	ids := func(is []*Issue) (out []string) {
		for _, i := range is {
			out = append(out, i.ID)
		}
		return
	}
	if got := ids(s.BlockChain(s.ByID["x-a"])); len(got) != 2 || got[0] != "x-b" || got[1] != "x-c" {
		t.Errorf("chain of x-a: %v", got)
	}
	if got := ids(s.BlockChain(s.ByID["x-p.1"])); len(got) != 1 || got[0] != "x-c" {
		t.Errorf("chain through blocked parent: %v", got)
	}
	var closable []string
	for _, r := range s.Triage() {
		if r.ID == "closable-epic" {
			closable = ids(r.Issues)
		}
	}
	if len(closable) != 1 || closable[0] != "x-e" {
		t.Errorf("closable epics: %v", closable)
	}
}
