package bd

import (
	"sort"
	"time"
)

// TriageRule is one hygiene check from the beads workflow rules.
type TriageRule struct {
	ID      string `json:"rule"`
	Name    string `json:"name"`
	Fix     string `json:"fix"` // bd command template; {id} is the issue
	Key     string `json:"-"`   // TUI key that fixes it, if any
	matches func(s *Snapshot, is *Issue) bool
}

var TriageRules = []TriageRule{
	{"no-machine-label", "no machine label", "bd update {id} --add-label machine:<host>", "m", func(s *Snapshot, is *Issue) bool {
		return (is.Status == "open" || is.Status == "in_progress") && is.LabelWithPrefix("machine:") == ""
	}},
	{"root-without-external-ref", "root without external ref", "bd update {id} --external-ref <wl-key>", "x", func(s *Snapshot, is *Issue) bool {
		return is.Parent == "" && !is.Closed() && is.ExternalRef == ""
	}},
	{"root-without-estimate", "root without estimate", "bd update {id} -e <minutes>", "e", func(s *Snapshot, is *Issue) bool {
		return is.Parent == "" && !is.Closed() && is.EstimatedMinutes == 0
	}},
	{"not-under-epic", "not under an epic", "bd update {id} --parent <epic-id>", "", func(s *Snapshot, is *Issue) bool {
		return !is.Closed() && is.IssueType != "epic" && s.Epic(is) == nil
	}},
	{"stale-in-progress", "in progress, idle 3+ days", "bd update {id} --status open|blocked|deferred, or bd close {id}", "s", func(s *Snapshot, is *Issue) bool {
		return is.Status == "in_progress" && time.Since(is.UpdatedAt) > 72*time.Hour
	}},
	{"repo-task-without-refs", "repo task without code refs", `bd update {id} --metadata '{"refs":[{"repo":"~/repo/x","file":"path","symbol":"Name","line":1}]}'`, "", func(s *Snapshot, is *Issue) bool {
		return is.Status == "in_progress" && is.IssueType != "epic" && is.LabelWithPrefix("repo:") != "" && len(is.Refs()) == 0
	}},
}

type TriageResult struct {
	TriageRule
	Issues []*Issue `json:"issues"`
}

// Triage runs every rule; issues inside a rule are ordered by priority, then id.
func (s *Snapshot) Triage() []TriageResult {
	var out []TriageResult
	for _, r := range TriageRules {
		var hits []*Issue
		for _, is := range s.Issues {
			if r.matches(s, is) {
				hits = append(hits, is)
			}
		}
		sort.SliceStable(hits, func(i, j int) bool {
			if hits[i].Priority != hits[j].Priority {
				return hits[i].Priority < hits[j].Priority
			}
			return IDLess(hits[i].ID, hits[j].ID)
		})
		out = append(out, TriageResult{r, hits})
	}
	return out
}
