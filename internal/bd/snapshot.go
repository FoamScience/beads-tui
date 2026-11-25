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
	// Ephemeral indexes the wisp rows on their own, so no rule or view outside Workflows sees them.
	Ephemeral *Snapshot
	// Gates indexes gate rows (ephemeral or not) on their own, for the same reason: they are
	// conditions, not work items, and show only as blockers outside Workflows.
	Gates *Snapshot
	// peers are the other sides of the split; blocker and dependent lookups cross them.
	peers      []*Snapshot
	dependents map[string][]Dependency // depends-on id -> non-parent edges pointing at it
}

func NewSnapshot(issues []Issue) *Snapshot {
	var kept, eph, gates []*Issue
	for i := range issues {
		switch {
		case issues[i].IssueType == "gate":
			gates = append(gates, &issues[i])
		case issues[i].Ephemeral:
			eph = append(eph, &issues[i])
		default:
			kept = append(kept, &issues[i])
		}
	}
	s := index(kept)
	s.Ephemeral, s.Gates = index(eph), index(gates)
	s.peers = []*Snapshot{s.Ephemeral, s.Gates}
	s.Ephemeral.peers = []*Snapshot{s, s.Gates}
	s.Gates.peers = []*Snapshot{s, s.Ephemeral}
	// a formula's gate step stays a child of its molecule or wisp, so progress and walks count it
	for _, g := range gates {
		if p := s.Find(g.Parent); p != nil {
			idx := s.Of(p)
			kids := append(idx.Children[p.ID], g)
			sort.Slice(kids, func(a, b int) bool { return IDLess(kids[a].ID, kids[b].ID) })
			idx.Children[p.ID] = kids
		}
	}
	return s
}

// Find looks an id up on every side of the split.
func (s *Snapshot) Find(id string) *Issue {
	if is := s.ByID[id]; is != nil {
		return is
	}
	for _, p := range s.peers {
		if is := p.ByID[id]; is != nil {
			return is
		}
	}
	return nil
}

// Of returns the index holding the issue: s, or another side of the split.
func (s *Snapshot) Of(is *Issue) *Snapshot {
	if s.ByID[is.ID] != nil {
		return s
	}
	for _, p := range s.peers {
		if p.ByID[is.ID] != nil {
			return p
		}
	}
	return s
}

func index(issues []*Issue) *Snapshot {
	s := &Snapshot{
		Issues:     issues,
		ByID:       make(map[string]*Issue, len(issues)),
		Children:   map[string][]*Issue{},
		Loaded:     time.Now(),
		dependents: map[string][]Dependency{},
	}
	for _, is := range issues {
		s.ByID[is.ID] = is
		for _, d := range is.Dependencies {
			if d.Type != "parent-child" {
				s.dependents[d.DependsOnID] = append(s.dependents[d.DependsOnID], d)
			}
		}
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
		if t := s.Find(d.DependsOnID); t != nil && !t.Closed() {
			out = append(out, t)
		}
	}
	return out
}

// Dependents returns issues on any side of the split that depend on this one through any
// non-parent edge.
func (s *Snapshot) Dependents(id string) []Dependency {
	var out []Dependency
	for _, idx := range append([]*Snapshot{s}, s.peers...) {
		out = append(out, idx.dependents[id]...)
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
	for p := is; p != nil; p = s.Find(p.Parent) {
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

// Ready reports whether the issue would show in bd ready: open, not deferred, no blocked ancestor.
// Gates never do: they are conditions, not work.
func (s *Snapshot) Ready(is *Issue) bool {
	if is.Status != "open" || is.IssueType == "epic" || is.IssueType == "gate" {
		return false
	}
	if is.DeferUntil != nil && is.DeferUntil.After(time.Now()) {
		return false
	}
	for p := is; p != nil; p = s.Find(p.Parent) {
		if s.IsBlocked(p) {
			return false
		}
	}
	return true
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
