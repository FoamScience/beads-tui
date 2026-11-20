// Package goals reads the hand-edited goals.toml (cycles of weighted goals over beads) and the
// goals.accepted.toml sidecar bt writes, and scores them against a bd snapshot.
package goals

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/FoamScience/beads-tui/internal/bd"
)

type File struct {
	Cycles []*Cycle `toml:"cycle"`
}

type Cycle struct {
	ID    string    `toml:"id"`
	Start time.Time `toml:"start"`
	End   time.Time `toml:"end"`
	Doc   string    `toml:"doc"`
	Goals []*Goal   `toml:"goal"`
}

type Goal struct {
	ID      string  `toml:"id"`
	Title   string  `toml:"title"`
	KR      string  `toml:"kr"`
	Type    string  `toml:"type"`    // performance | ai | competence
	Scoring string  `toml:"scoring"` // points (the default, filled in by Load) | ladder
	Items   []*Item `toml:"items"`
}

type Item struct {
	Label string  `toml:"label"`
	Bead  string  `toml:"bead"`
	Pts   float64 `toml:"pts"`
}

func (g *Goal) Ladder() bool { return g.Scoring == "ladder" }

// Sidecar is goals.accepted.toml, keyed by Key: the only file bt writes.
type Sidecar struct {
	Accepted map[string]string `toml:"accepted"` // key -> YYYY-MM-DD
	Beads    map[string]string `toml:"beads"`    // key -> bead id bt created for an item without one
}

// Key names an item in the sidecar.
func Key(c *Cycle, g *Goal, it *Item) string { return c.ID + "/" + g.ID + "/" + it.Label }

func Path() string {
	if p := os.Getenv("BT_GOALS"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "bt", "goals.toml")
}

func SidecarPath(path string) string {
	return filepath.Join(filepath.Dir(path), "goals.accepted.toml")
}

// Set is goals.toml with its sidecar; nil File means there is no goals.toml.
type Set struct {
	Path    string
	File    *File
	Side    Sidecar
	Warning []string
}

func Load(path string) (*Set, error) {
	s := &Set{Path: path, Side: Sidecar{Accepted: map[string]string{}, Beads: map[string]string{}}}
	var f File
	md, err := toml.DecodeFile(path, &f)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, nil
	case err != nil:
		return s, err
	case len(md.Undecoded()) > 0:
		return s, fmt.Errorf("goals.toml: unknown keys: %v", md.Undecoded())
	}
	s.File = &f
	if _, err := toml.DecodeFile(SidecarPath(path), &s.Side); err != nil && !errors.Is(err, os.ErrNotExist) {
		return s, err
	}
	if s.Side.Accepted == nil {
		s.Side.Accepted = map[string]string{}
	}
	if s.Side.Beads == nil {
		s.Side.Beads = map[string]string{}
	}
	known := map[string]bool{}
	for _, c := range f.Cycles {
		for _, g := range c.Goals {
			switch g.Scoring {
			case "":
				g.Scoring = "points"
			case "points", "ladder":
			default:
				return s, fmt.Errorf("goals.toml: %s/%s: scoring %q is not points or ladder", c.ID, g.ID, g.Scoring)
			}
			if t := g.Total(); math.Abs(t-100) > 1e-9 {
				s.Warning = append(s.Warning, fmt.Sprintf("%s/%s points sum to %g, not 100", c.ID, g.ID, t))
			}
			for _, it := range g.Items {
				known[Key(c, g, it)] = true
			}
		}
	}
	// sidecar entries are keyed by item label: a renamed label leaves its entry behind
	for _, m := range []map[string]string{s.Side.Accepted, s.Side.Beads} {
		for _, k := range slices.Sorted(maps.Keys(m)) {
			if !known[k] {
				s.Warning = append(s.Warning, fmt.Sprintf("goals.accepted.toml: %q matches no item", k))
			}
		}
	}
	for _, k := range slices.Sorted(maps.Keys(s.Side.Accepted)) {
		if _, err := time.Parse(time.DateOnly, s.Side.Accepted[k]); err != nil {
			s.Warning = append(s.Warning, fmt.Sprintf("goals.accepted.toml: %q has date %q, not YYYY-MM-DD", k, s.Side.Accepted[k]))
		}
	}
	return s, nil
}

const sidecarHeader = "# written by bt: accepted goal items and beads it created for them; edit goals.toml instead\n"

func (s *Set) SaveSidecar() error {
	var b bytes.Buffer
	b.WriteString(sidecarHeader)
	if err := toml.NewEncoder(&b).Encode(s.Side); err != nil {
		return err
	}
	p := SidecarPath(s.Path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, b.Bytes(), 0o644)
}

// Current is the cycle containing now, else the nearest upcoming one, else the one that ended last.
func (s *Set) Current(now time.Time) int {
	if s.File == nil || len(s.File.Cycles) == 0 {
		return -1
	}
	best, upcoming := -1, -1
	for i, c := range s.File.Cycles {
		if c.Contains(now) {
			return i
		}
		if c.Start.After(now) && (upcoming < 0 || c.Start.Before(s.File.Cycles[upcoming].Start)) {
			upcoming = i
		}
		if best < 0 || c.End.After(s.File.Cycles[best].End) {
			best = i
		}
	}
	if upcoming >= 0 {
		return upcoming
	}
	return best
}

func day(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

// Contains counts the end date as a whole day.
func (c *Cycle) Contains(t time.Time) bool {
	return !t.Before(day(c.Start)) && t.Before(day(c.End).AddDate(0, 0, 1))
}

// Days is the cycle's length and the days elapsed by now, both in calendar days.
func (c *Cycle) Days(now time.Time) (elapsed, total int) {
	days := func(a, b time.Time) int { return int(math.Round(b.Sub(a).Hours() / 24)) }
	total = days(day(c.Start), day(c.End)) + 1
	return min(max(days(day(c.Start), day(now)), 0), total), total
}

func (c *Cycle) Past(now time.Time) bool { return !now.Before(day(c.End).AddDate(0, 0, 1)) }

// TimePct is how much of the cycle has elapsed, 0..100.
func (c *Cycle) TimePct(now time.Time) float64 {
	start, end := day(c.Start), day(c.End).AddDate(0, 0, 1)
	if !end.After(start) {
		return 0
	}
	return 100 * min(max(float64(now.Sub(start))/float64(end.Sub(start)), 0), 1)
}

func (g *Goal) Total() float64 {
	t := 0.0
	for _, it := range g.Items {
		t += it.Pts
	}
	return t
}

type State int

const (
	NoBead  State = iota // the item names no bead yet
	Missing              // its bead is not in the DB
	Open
	InProgress
	Done // bead closed, not yet accepted
	Accepted
	Locked // a ladder rung whose previous rung is not accepted
)

func (st State) String() string {
	return [...]string{"no_bead", "missing", "open", "in_progress", "done", "accepted", "locked"}[st]
}

// Bead is the item's bead id: the one in goals.toml, else one bt created for it.
func (s *Set) Bead(c *Cycle, g *Goal, it *Item) string {
	if it.Bead != "" {
		return it.Bead
	}
	return s.Side.Beads[Key(c, g, it)]
}

// AcceptedOn is the date the item was accepted, if it was; an unreadable date, or one after a past cycle's end, does not count.
func (s *Set) AcceptedOn(c *Cycle, g *Goal, it *Item) (string, bool) {
	d, ok := s.Side.Accepted[Key(c, g, it)]
	if !ok {
		return "", false
	}
	t, err := time.ParseInLocation(time.DateOnly, d, time.Local)
	if err != nil || c.Past(t) {
		return d, false
	}
	return d, true
}

func (s *Set) State(snap *bd.Snapshot, c *Cycle, g *Goal, i int) State {
	if g.Ladder() {
		for _, prev := range g.Items[:i] {
			if _, ok := s.AcceptedOn(c, g, prev); !ok {
				return Locked
			}
		}
	}
	it := g.Items[i]
	if _, ok := s.AcceptedOn(c, g, it); ok {
		return Accepted
	}
	id := s.Bead(c, g, it)
	if id == "" {
		return NoBead
	}
	is := snap.ByID[id]
	switch {
	case is == nil:
		return Missing
	case is.Closed():
		return Done
	case is.Status == "in_progress":
		return InProgress
	}
	return Open
}

// Score is a goal's accepted share and the closed-but-unconfirmed share on top, both in % of its points.
func (s *Set) Score(snap *bd.Snapshot, c *Cycle, g *Goal) (earned, done float64) {
	total := g.Total()
	if total <= 0 {
		return 0, 0
	}
	for i, it := range g.Items {
		switch s.State(snap, c, g, i) {
		case Accepted:
			earned += it.Pts
		case Done:
			done += it.Pts
		}
	}
	return 100 * earned / total, 100 * done / total
}

// CycleScore averages the goals' earned %.
func (s *Set) CycleScore(snap *bd.Snapshot, c *Cycle) float64 {
	if len(c.Goals) == 0 {
		return 0
	}
	sum := 0.0
	for _, g := range c.Goals {
		e, _ := s.Score(snap, c, g)
		sum += e
	}
	return sum / float64(len(c.Goals))
}

// Behind flags earned trailing elapsed time by more than 20 points.
func Behind(timePct, earned float64) bool { return timePct-earned > 20 }

// Tags maps every item bead of the cycle to the ids of the goals it scores for.
func (s *Set) Tags(c *Cycle) map[string][]string {
	out := map[string][]string{}
	if c == nil {
		return out
	}
	for _, g := range c.Goals {
		for _, it := range g.Items {
			if id := s.Bead(c, g, it); id != "" && !slices.Contains(out[id], g.ID) {
				out[id] = append(out[id], g.ID)
			}
		}
	}
	return out
}
