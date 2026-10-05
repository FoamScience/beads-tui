package bd

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// Snapshot is an immutable, indexed view of every issue in the DB.
type Snapshot struct {
	Issues   []*Issue
	ByID     map[string]*Issue
	Children map[string][]*Issue // parent id -> children, ordered by id
	Loaded   time.Time
}

func NewSnapshot(issues []Issue) *Snapshot {
	s := &Snapshot{
		ByID:     make(map[string]*Issue, len(issues)),
		Children: map[string][]*Issue{},
		Loaded:   time.Now(),
	}
	for i := range issues {
		is := &issues[i]
		s.Issues = append(s.Issues, is)
		s.ByID[is.ID] = is
	}
	for _, is := range s.Issues {
		if is.Parent != "" {
			s.Children[is.Parent] = append(s.Children[is.Parent], is)
		}
	}
	for _, kids := range s.Children {
		sort.Slice(kids, func(a, b int) bool { return IDLess(kids[a].ID, kids[b].ID) })
	}
	return s
}

var blockingTypes = map[string]bool{"blocks": true, "waits-for": true, "conditional-blocks": true}

// Blocking reports whether a dependency type keeps the dependent out of bd ready.
func Blocking(typ string) bool { return blockingTypes[typ] }

// Blockers returns the open issues this one waits on.
func (s *Snapshot) Blockers(is *Issue) []*Issue {
	var out []*Issue
	for _, d := range is.Dependencies {
		if !blockingTypes[d.Type] {
			continue
		}
		if t, ok := s.ByID[d.DependsOnID]; ok && !t.Closed() {
			out = append(out, t)
		}
	}
	return out
}

// Dependents returns issues that depend on this one through any non-parent edge.
func (s *Snapshot) Dependents(id string) []Dependency {
	var out []Dependency
	for _, is := range s.Issues {
		for _, d := range is.Dependencies {
			if d.DependsOnID == id && d.Type != "parent-child" {
				out = append(out, d)
			}
		}
	}
	return out
}

func (s *Snapshot) IsBlocked(is *Issue) bool { return len(s.Blockers(is)) > 0 }

// BlockChain follows open blockers (the issue's own, then its ancestors') down to the first
// one that is not blocked itself: the work that has to move before this issue can.
func (s *Snapshot) BlockChain(is *Issue) []*Issue {
	var chain []*Issue
	seen := map[string]bool{is.ID: true}
	for cur := is; ; {
		next := s.firstBlocker(cur)
		if next == nil || seen[next.ID] {
			return chain
		}
		seen[next.ID] = true
		chain = append(chain, next)
		cur = next
	}
}

func (s *Snapshot) firstBlocker(is *Issue) *Issue {
	for p := is; p != nil; p = s.ByID[p.Parent] {
		if b := s.Blockers(p); len(b) > 0 {
			return b[0]
		}
	}
	return nil
}

// Epic returns the nearest epic ancestor, or nil.
func (s *Snapshot) Epic(is *Issue) *Issue {
	for p := s.ByID[is.Parent]; p != nil; p = s.ByID[p.Parent] {
		if p.IssueType == "epic" {
			return p
		}
	}
	return nil
}

// Root returns the top-most ancestor (the issue itself when it has no parent).
func (s *Snapshot) Root(is *Issue) *Issue {
	for p := s.ByID[is.Parent]; p != nil; p = s.ByID[p.Parent] {
		is = p
	}
	return is
}

// Progress counts closed and total descendants.
func (s *Snapshot) Progress(id string) (closed, total int) {
	for _, c := range s.Children[id] {
		total++
		if c.Closed() {
			closed++
		}
		cc, ct := s.Progress(c.ID)
		closed += cc
		total += ct
	}
	return
}

// IDLess orders hierarchical ids numerically per segment (x.2 < x.10).
func IDLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] == bs[i] {
			continue
		}
		an, aerr := strconv.Atoi(as[i])
		bn, berr := strconv.Atoi(bs[i])
		if aerr == nil && berr == nil {
			return an < bn
		}
		return as[i] < bs[i]
	}
	return len(as) < len(bs)
}
