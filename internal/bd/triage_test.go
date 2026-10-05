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
