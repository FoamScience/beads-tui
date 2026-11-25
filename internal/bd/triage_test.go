package bd

import (
	"slices"
	"testing"
	"time"
)

func TestTriage(t *testing.T) {
	old := time.Now().Add(-100 * time.Hour)
	s := NewSnapshot([]Issue{
		{ID: "x-e", IssueType: "epic", Status: "open", Labels: []string{"machine:h"}, ExternalRef: "K-1", EstimatedMinutes: 60},
		{ID: "x-e.1", Parent: "x-e", IssueType: "task", Status: "in_progress", UpdatedAt: old, Labels: []string{"repo:r"}},
		{ID: "x-e.2", Parent: "x-e", IssueType: "task", Status: "in_progress", UpdatedAt: time.Now(), Labels: []string{"machine:h"}, Metadata: []byte(`{"claude_session.aaaa_bbbb":"none"}`)},
		{ID: "x-loose", IssueType: "task", Status: "open", Labels: []string{"machine:h"}},
		{ID: "x-done", IssueType: "task", Status: "closed"},
	})
	want := map[string][]string{
		"no-machine-label":          {"x-e.1"},
		"root-without-external-ref": {"x-loose"},
		"root-without-estimate":     {"x-loose"},
		"not-under-epic":            {"x-loose"},
		"stale-in-progress":         {"x-e.1"},
		"not-claimed-by-session":    {"x-e.1", "x-loose"},
		"repo-task-without-refs":    {"x-e.1"},
		"closable-epic":             nil,
	}
	for _, r := range s.Triage() {
		var got []string
		for _, is := range r.Issues {
			got = append(got, is.ID)
		}
		if !slices.Equal(got, want[r.ID]) {
			t.Errorf("%s: got %v want %v", r.ID, got, want[r.ID])
		}
	}
}

func TestMeasure(t *testing.T) {
	now := time.Now()
	day := 24 * time.Hour
	s := NewSnapshot([]Issue{
		{ID: "a", Status: "closed", UpdatedAt: now.Add(-90 * day)},
		{ID: "b", Status: "open", Priority: 1, Description: "d", EstimatedMinutes: 30, UpdatedAt: now.Add(-40 * day)},
		{ID: "c", Status: "in_progress", Priority: 2, UpdatedAt: now.Add(-20 * day)},
		{ID: "d", Status: "in_progress", Assignee: "me", Description: "d", EstimatedMinutes: 15, Priority: 3, UpdatedAt: now},
		{ID: "e", Status: "deferred", Priority: 0, Description: " ", UpdatedAt: now.Add(-31 * day)},
		{ID: "f", Status: "open", Priority: 2, Description: "d", EstimatedMinutes: 5, UpdatedAt: now,
			Dependencies: []Dependency{{IssueID: "f", DependsOnID: "c", Type: "blocks"}}},
	})
	got := s.Measure(s.Issues, now)
	want := Hygiene{Total: 6, Closed: 1, Open: 1, InProgress: 2, Blocked: 1, Deferred: 1,
		Stale30: 1, Idle14: 2, NoEstimate: 2, HighPriority: 2, Unassigned: 1, NoDescr: 2}
	if got != want {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	if got.Active() != 5 {
		t.Errorf("active %d", got.Active())
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

func TestOnMachine(t *testing.T) {
	here := &Issue{Labels: []string{"machine:h"}}
	other := &Issue{Labels: []string{"machine:o"}}
	none := &Issue{}
	for _, c := range []struct {
		is   *Issue
		m    string
		want bool
	}{{here, "h", true}, {other, "h", false}, {none, "h", true}, {other, AllMachines, true}} {
		if got := c.is.OnMachine(c.m); got != c.want {
			t.Errorf("%v on %s: got %v", c.is.Labels, c.m, got)
		}
	}
}
