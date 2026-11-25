package bd

import (
	"sort"
	"strings"
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
	{"closable-epic", "epic with every child closed", "bd close {id}", "c", func(s *Snapshot, is *Issue) bool {
		if is.IssueType != "epic" || is.Closed() || len(s.Children[is.ID]) == 0 {
			return false
		}
		for _, c := range s.Children[is.ID] {
			if !c.Closed() {
				return false
			}
		}
		return true
	}},
	{"not-claimed-by-session", "open or in progress, not claimed by a session", "bd update {id} --status in_progress, from the agent session working it", "", func(s *Snapshot, is *Issue) bool {
		return (is.Status == "open" || is.Status == "in_progress") && is.IssueType != "epic" && !is.HasSession()
	}},
	{"repo-task-without-refs", "repo task without code refs", `bd update {id} --metadata '{"refs":[{"repo":"~/repo/x","file":"path","symbol":"Name","line":1}]}'`, "", func(s *Snapshot, is *Issue) bool {
		return is.Status == "in_progress" && is.IssueType != "epic" && is.LabelWithPrefix("repo:") != "" && len(is.Refs()) == 0
	}},
}

// Hygiene summarises the health of a set of issues; every count past Closed is over the not-closed ones.
type Hygiene struct {
	Total, Closed                                 int
	Open, InProgress, Blocked, Deferred, Pinned   int
	Stale30, Idle14                               int
	NoEstimate, HighPriority, Unassigned, NoDescr int
}

func (h Hygiene) Active() int { return h.Total - h.Closed }

// Measure computes Hygiene over issues as of now. Open issues waiting on a dependency count as
// blocked, as the list glyphs show them. Stale means no update for 30 days, idle for 14; deferred
// and pinned issues are left alone on purpose, so neither counts them.
func (s *Snapshot) Measure(issues []*Issue, now time.Time) Hygiene {
	var h Hygiene
	for _, is := range issues {
		h.Total++
		if is.Closed() {
			h.Closed++
			continue
		}
		switch is.Status {
		case "open":
			if s.IsBlocked(is) {
				h.Blocked++
			} else {
				h.Open++
			}
		case "in_progress":
			h.InProgress++
			if is.Assignee == "" {
				h.Unassigned++
			}
		case "blocked":
			h.Blocked++
		case "deferred":
			h.Deferred++
		case "pinned":
			h.Pinned++
		}
		if idle := now.Sub(is.UpdatedAt); is.Status != "deferred" && is.Status != "pinned" {
			if idle >= 30*24*time.Hour {
				h.Stale30++
			}
			if idle >= 14*24*time.Hour {
				h.Idle14++
			}
		}
		if is.EstimatedMinutes == 0 {
			h.NoEstimate++
		}
		if is.Priority <= 1 {
			h.HighPriority++
		}
		if strings.TrimSpace(is.Description) == "" {
			h.NoDescr++
		}
	}
	return h
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
