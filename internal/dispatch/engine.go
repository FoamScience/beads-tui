package dispatch

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/FoamScience/beads-tui/internal/bd"
)

const (
	LabelLane    = "lane:"
	LabelPartial = "partial"

	pickupTimeout = 3 * time.Minute // a sent bead not claimed by then is offered again
	spawnTimeout  = 2 * time.Minute // a spawned session with no pane by then has failed
)

type Progress string

const (
	Queued     Progress = "queued"
	Waiting    Progress = "waiting"
	InProgress Progress = "in_progress"
	Partial    Progress = "partial"
	Completed  Progress = "completed"
	Other      Progress = "other" // epics, deferred, pinned ledgers
)

func StateOf(s *bd.Snapshot, is *bd.Issue) Progress {
	switch {
	case is.IssueType == "epic" || is.Status == "pinned" || is.Status == "deferred":
		return Other
	case is.Closed():
		return Completed
	case is.HasLabel(LabelPartial):
		return Partial
	case is.Status == "in_progress":
		return InProgress
	case s.Ready(is):
		return Queued
	default:
		return Waiting
	}
}

// Pane is the part of a herdr pane the engine needs; Title carries the Claude session name.
type Pane struct {
	ID        string
	Agent     string
	Status    string // idle, working, blocked, unknown
	Title     string
	Workspace string
	Session   string // Claude session uuid
	Tab       string
}

// State is what the engine remembers between ticks, plus the allow-list read from dispatch.toml.
type State struct {
	Allow  Allow
	Sent   map[string]sent // lane -> bead sent or spawned and not yet claimed
	Failed map[string]bool // beads whose spawned session never came up; not retried until bt dispatch restarts
}

type sent struct {
	ID    string
	At    time.Time
	Spawn bool // the bead got a new session rather than its idle one
}

func NewState() *State {
	return &State{Sent: map[string]sent{}, Failed: map[string]bool{}}
}

type Kind string

const (
	Send  Kind = "send"  // type /work <bead> into the bead's idle session
	Spawn Kind = "spawn" // start a session named after the bead in a new herdr tab, with /work <bead> as its prompt
	Close Kind = "close" // close the idle session of a closed bead, and its tab when it is the tab's only pane
	Note  Kind = "note"  // event for the log only
)

type Action struct {
	Kind Kind
	Lane string
	Bead string
	Pane string
	Tab  string // Close only: the tab to close, empty when other panes share it
	Text string
}

func (a Action) String() string {
	switch a.Kind {
	case Send:
		return fmt.Sprintf("send  %-16s %s -> %s", a.Lane, a.Bead, a.Pane)
	case Spawn:
		return fmt.Sprintf("spawn %-16s %s", a.Lane, a.Bead)
	case Close:
		return fmt.Sprintf("close %-16s %s (closed) pane %s", a.Lane, a.Bead, a.Pane)
	}
	return fmt.Sprintf("note  %-16s %s", a.Lane, a.Text)
}

// Route picks the lane for a bead: an explicit lane:<name> label wins, else the single lane whose
// takes matches the bead's labels or epic. No match or several matches leave it unrouted ("").
func Route(c *Config, s *bd.Snapshot, is *bd.Issue) string {
	if l := is.LabelWithPrefix(LabelLane); l != "" {
		if _, ok := c.Lanes[l]; ok {
			return l
		}
		return ""
	}
	epics := map[string]bool{}
	for p := s.ByID[is.Parent]; p != nil; p = s.ByID[p.Parent] {
		epics[p.ID] = true
	}
	match := ""
	for _, name := range c.Names() {
		if takes(c.Lanes[name].Takes, is, epics) {
			if match != "" {
				return ""
			}
			match = name
		}
	}
	return match
}

func takes(t Takes, is *bd.Issue, epics map[string]bool) bool {
	for _, l := range t.Labels {
		if is.HasLabel(l) {
			return true
		}
	}
	for _, e := range t.Epics {
		if epics[e] {
			return true
		}
	}
	return false
}

// laneOf is the lane a bead belongs to: the lane it is assigned to, else its route.
func laneOf(c *Config, s *bd.Snapshot, is *bd.Issue) string {
	if _, ok := c.Lanes[is.Assignee]; ok {
		return is.Assignee
	}
	return Route(c, s, is)
}

// SessionPane finds the live Claude pane whose session name is name; a bead's session is named by its id.
func SessionPane(panes []Pane, name string) (Pane, bool) {
	for _, p := range panes {
		if p.Agent == "claude" && p.Title == name {
			return p, true
		}
	}
	return Pane{}, false
}

// LaneView is a lane's current picture: its pane, the bead it works, its queue and progress counts.
type LaneView struct {
	Lane   *Lane
	Pane   Pane // the session of the active bead, else a busy session of another of its beads
	Live   bool
	Active string      // an in_progress bead the lane is working
	Queue  []*bd.Issue // queued and unblocked partial beads in dispatch order, allowed or not
	Count  map[Progress]int
	Top    int // highest priority (lowest number) among its open beads, -1 when it has none
}

// workText is what a bead's session is sent: /work, the bead and its lane, plus the live ledgers the bead may have to update.
func workText(lane string, s *bd.Snapshot, is *bd.Issue) string {
	if l := Ledgers(s, is); len(l) > 0 {
		return "/work " + is.ID + " (lane: " + lane + "; live ledgers: " + strings.Join(l, ", ") + ")"
	}
	return "/work " + is.ID + " (lane: " + lane + ")"
}

// Lanes builds every lane's view. A bead assigned to a lane belongs to it whatever its routing says.
// Only leaf beads are queued.
func Lanes(c *Config, s *bd.Snapshot, panes []Pane, a Allow) map[string]*LaneView {
	vs := map[string]*LaneView{}
	for name, l := range c.Lanes {
		vs[name] = &LaneView{Lane: l, Count: map[Progress]int{}, Top: -1}
	}
	owner := map[string]string{}
	for _, is := range s.Issues {
		lane := laneOf(c, s, is)
		if lane == "" {
			continue
		}
		owner[is.ID] = lane
		v, st := vs[lane], StateOf(s, is)
		v.Count[st]++
		if st != Completed && st != Other && (v.Top < 0 || is.Priority < v.Top) {
			v.Top = is.Priority
		}
		switch {
		case st == InProgress:
			v.Active = is.ID
		case len(s.Children[is.ID]) > 0: // a parent progresses through its children, never as one unit
		case st == Queued, st == Partial && !s.IsBlocked(is):
			v.Queue = append(v.Queue, is)
		}
	}
	for _, p := range panes {
		if v := vs[owner[p.Title]]; v != nil && p.Agent == "claude" && (p.Title == v.Active || !v.Live && p.Status != "idle") {
			v.Pane, v.Live = p, true
		}
	}
	for _, v := range vs {
		sort.SliceStable(v.Queue, func(i, j int) bool { return before(a, v.Queue[i], v.Queue[j]) })
	}
	return vs
}

// Plan decides this tick's actions and updates st's memory of sent beads and failed spawns.
func Plan(c *Config, s *bd.Snapshot, panes []Pane, st *State, now time.Time) []Action {
	var acts []Action
	vs := Lanes(c, s, panes, st.Allow)

	perTab := map[string]int{}
	for _, p := range panes {
		perTab[p.Tab]++
	}
	for _, p := range panes {
		is := s.ByID[p.Title]
		if p.Agent != "claude" || p.Status != "idle" || is == nil || !is.Closed() || laneOf(c, s, is) == "" {
			continue
		}
		a := Action{Kind: Close, Lane: laneOf(c, s, is), Bead: is.ID, Pane: p.ID}
		if perTab[p.Tab] == 1 {
			a.Tab = p.Tab
		}
		acts = append(acts, a)
	}

	busy := 0
	for _, name := range c.Names() {
		p, live := vs[name].Pane, vs[name].Live
		if snt, ok := st.Sent[name]; ok {
			is := s.ByID[snt.ID]
			_, up := SessionPane(panes, snt.ID)
			switch {
			case is == nil || is.Status != "open" && !is.HasLabel(LabelPartial):
				delete(st.Sent, name)
			case snt.Spawn && !up && now.Sub(snt.At) > spawnTimeout:
				acts = append(acts, Action{Kind: Note, Lane: name, Bead: snt.ID, Text: snt.ID + " session did not come up, not retrying until bt dispatch restarts"})
				st.Failed[snt.ID] = true
				delete(st.Sent, name)
			case now.Sub(snt.At) > pickupTimeout:
				acts = append(acts, Action{Kind: Note, Lane: name, Bead: snt.ID, Text: snt.ID + " not picked up, offering again"})
				delete(st.Sent, name)
			}
		}
		if _, ok := st.Sent[name]; ok || vs[name].Active != "" || live && p.Status != "idle" {
			busy++
		}
	}

	for _, name := range c.Names() {
		v := vs[name]
		var next *bd.Issue
		for _, is := range v.Queue {
			if st.Allow.Permits(s, is, name) {
				next = is
				break
			}
		}
		if next == nil || st.Failed[next.ID] {
			continue
		}
		if _, ok := st.Sent[name]; ok || v.Active != "" {
			continue
		}
		if v.Live && v.Pane.Status != "idle" {
			continue
		}
		if c.MaxActive > 0 && busy >= c.MaxActive {
			break
		}
		text := workText(name, s, next)
		if p, ok := SessionPane(panes, next.ID); ok {
			st.Sent[name] = sent{ID: next.ID, At: now}
			acts = append(acts, Action{Kind: Send, Lane: name, Bead: next.ID, Pane: p.ID, Text: text})
		} else if c.Lanes[name].Workspace == "" {
			acts = append(acts, Action{Kind: Note, Lane: name, Bead: next.ID, Text: "no workspace to start a session for allowed " + next.ID})
			continue
		} else {
			st.Sent[name] = sent{ID: next.ID, At: now, Spawn: true}
			acts = append(acts, Action{Kind: Spawn, Lane: name, Bead: next.ID, Text: text})
		}
		busy++
	}
	return acts
}

// before orders a lane's queue: partial work first, then beads listed in dispatch.toml, then priority, then id.
func before(al Allow, a, b *bd.Issue) bool {
	if pa, pb := a.HasLabel(LabelPartial), b.HasLabel(LabelPartial); pa != pb {
		return pa
	}
	if na, nb := slices.Contains(al.Beads, a.ID), slices.Contains(al.Beads, b.ID); na != nb {
		return na
	}
	if a.Priority != b.Priority {
		return a.Priority < b.Priority
	}
	return bd.IDLess(a.ID, b.ID)
}
