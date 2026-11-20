package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/FoamScience/beads-tui/internal/dispatch"
	"github.com/FoamScience/beads-tui/internal/goals"
	"github.com/FoamScience/beads-tui/internal/ui"
)

var version = "dev" // set by make via -ldflags

const usage = `usage:
  bt                  open the TUI
  bt triage [--json] [--machine [<host>|all]]
                      print hygiene violations (JSON for agents); --machine keeps
                      beads of this host (or <host>) plus unlabelled ones
  bt goals [--json]   print the current goal cycle's goals, items, states and scores
                      from goals.toml (BT_GOALS overrides its path)
  bt dispatch         start a session per allowed bead, one bead per lane at a time;
                      the screen edits the allow-list
  bt dispatch --dry-run
                      print lanes and what one tick would do
  bt ledger checkout <id>
                      copy a live ledger's description to a file to edit; prints its path
  bt ledger commit <path>
                      write the edited ledger back; exits 13, changing nothing,
                      when another session updated it since the checkout
  bt --version        print the version`

func main() {
	c := bd.NewClient()
	switch {
	case len(os.Args) == 1:
		if _, err := tea.NewProgram(ui.New(c)).Run(); err != nil {
			fail(err)
		}
	case os.Args[1] == "--version" || os.Args[1] == "version":
		fmt.Println("bt", version)
	case os.Args[1] == "triage":
		asJSON, machine, err := triageArgs(os.Args[2:])
		if err != nil {
			fail(err)
		}
		if err := triage(c, asJSON, machine); err != nil {
			fail(err)
		}
	case os.Args[1] == "goals":
		if len(os.Args) > 3 || (len(os.Args) == 3 && os.Args[2] != "--json") {
			fail(fmt.Errorf("%s", usage))
		}
		if err := printGoals(c, len(os.Args) == 3); err != nil {
			fail(err)
		}
	case os.Args[1] == "dispatch":
		dry := len(os.Args) > 2 && os.Args[2] == "--dry-run"
		if len(os.Args) > 3 || (len(os.Args) == 3 && !dry) {
			fail(fmt.Errorf("%s", usage))
		}
		if dry {
			if err := dispatch.Run(c, os.Stdout); err != nil {
				fail(err)
			}
			return
		}
		e, err := dispatch.NewEngine(c, false)
		if err != nil {
			fail(err)
		}
		defer e.Close()
		if _, err := tea.NewProgram(ui.NewDispatch(e)).Run(); err != nil {
			fail(err)
		}
	case os.Args[1] == "ledger" && len(os.Args) == 4 && os.Args[2] == "checkout":
		path, err := c.LedgerCheckout(os.Args[3])
		if err != nil {
			fail(err)
		}
		fmt.Println(path)
	case os.Args[1] == "ledger" && len(os.Args) == 4 && os.Args[2] == "commit":
		if err := c.LedgerCommit(os.Args[3]); err != nil {
			fmt.Fprintln(os.Stderr, "bt:", err)
			if errors.Is(err, bd.ErrLedgerStale) {
				os.Exit(13)
			}
			os.Exit(1)
		}
	default:
		fmt.Println(usage)
		if os.Args[1] != "-h" && os.Args[1] != "--help" {
			os.Exit(2)
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "bt:", err)
	os.Exit(1)
}

type triageIssue struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	Status           string    `json:"status"`
	Type             string    `json:"type"`
	Priority         int       `json:"priority"`
	Parent           string    `json:"parent,omitempty"`
	Epic             string    `json:"epic,omitempty"`
	Labels           []string  `json:"labels,omitempty"`
	ExternalRef      string    `json:"external_ref,omitempty"`
	EstimatedMinutes int       `json:"estimated_minutes,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
	Fix              string    `json:"fix"`
}

type triageRule struct {
	Rule   string        `json:"rule"`
	Name   string        `json:"name"`
	Count  int           `json:"count"`
	Issues []triageIssue `json:"issues"`
}

// triageArgs parses [--json] [--machine [<host>|all]]; a bare --machine means this host.
func triageArgs(args []string) (asJSON bool, machine string, err error) {
	machine = bd.AllMachines
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			asJSON = true
		case "--machine":
			machine, _ = os.Hostname()
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				machine = args[i]
			}
		default:
			return false, "", fmt.Errorf("%s", usage)
		}
	}
	return asJSON, machine, nil
}

func triage(c bd.Client, asJSON bool, machine string) error {
	issues, err := c.List()
	if err != nil {
		return err
	}
	s := bd.NewSnapshot(issues)
	host, _ := os.Hostname()
	machines := map[string]bool{}
	for _, is := range s.Issues {
		if m := is.LabelWithPrefix("machine:"); m != "" {
			machines[m] = true
		}
	}
	known := slices.Sorted(maps.Keys(machines))
	var rules []triageRule
	for _, r := range s.Triage() {
		r.Issues = slices.DeleteFunc(r.Issues, func(is *bd.Issue) bool { return !is.OnMachine(machine) })
		tr := triageRule{Rule: r.ID, Name: r.Name, Count: len(r.Issues), Issues: []triageIssue{}}
		for _, is := range r.Issues {
			ti := triageIssue{
				ID: is.ID, Title: is.Title, Status: is.Status, Type: is.IssueType, Priority: is.Priority,
				Parent: is.Parent, Labels: is.Labels, ExternalRef: is.ExternalRef,
				EstimatedMinutes: is.EstimatedMinutes, UpdatedAt: is.UpdatedAt,
				Fix: strings.ReplaceAll(r.Fix, "{id}", is.ID),
			}
			if e := s.Epic(is); e != nil {
				ti.Epic = e.ID
			}
			tr.Issues = append(tr.Issues, ti)
		}
		rules = append(rules, tr)
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"beads_dir": c.Dir, "host": host, "machine": machine, "known_machines": known, "generated_at": time.Now().UTC(), "rules": rules})
	}
	for _, r := range rules {
		if r.Count == 0 {
			continue
		}
		fmt.Printf("%s (%d)\n", r.Name, r.Count)
		for _, is := range r.Issues {
			fmt.Printf("  %-16s P%d %-11s %s\n", is.ID, is.Priority, is.Status, is.Title)
		}
		fmt.Println()
	}
	return nil
}

type goalItem struct {
	Label      string  `json:"label"`
	Bead       string  `json:"bead,omitempty"`
	Title      string  `json:"title,omitempty"`
	Pts        float64 `json:"pts"`
	State      string  `json:"state"`
	AcceptedOn string  `json:"accepted_on,omitempty"`
}

type goalOut struct {
	ID      string     `json:"id"`
	Title   string     `json:"title"`
	KR      string     `json:"kr,omitempty"`
	Type    string     `json:"type,omitempty"`
	Scoring string     `json:"scoring"`
	Earned  float64    `json:"earned_pct"`
	Done    float64    `json:"done_unaccepted_pct"`
	Behind  bool       `json:"behind"`
	Items   []goalItem `json:"items"`
}

func printGoals(c bd.Client, asJSON bool) error {
	set, err := goals.Load(goals.Path())
	if err != nil {
		return err
	}
	now := time.Now()
	cy := set.Current(now)
	if cy < 0 {
		return fmt.Errorf("no goal cycles in %s", set.Path)
	}
	issues, err := c.List()
	if err != nil {
		return err
	}
	s := bd.NewSnapshot(issues)
	cyc := set.File.Cycles[cy]
	tp := cyc.TimePct(now)
	var out []goalOut
	for _, g := range cyc.Goals {
		earned, done := set.Score(s, cyc, g)
		o := goalOut{ID: g.ID, Title: g.Title, KR: g.KR, Type: g.Type, Scoring: g.Scoring, Earned: earned, Done: done,
			Behind: !cyc.Past(now) && goals.Behind(tp, earned), Items: []goalItem{}}
		for i, it := range g.Items {
			gi := goalItem{Label: it.Label, Bead: set.Bead(cyc, g, it), Pts: it.Pts, State: set.State(s, cyc, g, i).String()}
			if is := s.ByID[gi.Bead]; is != nil {
				gi.Title = is.Title
			}
			if d, ok := set.AcceptedOn(cyc, g, it); ok {
				gi.AcceptedOn = d
			}
			o.Items = append(o.Items, gi)
		}
		out = append(out, o)
	}
	score := set.CycleScore(s, cyc)
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"goals_file": set.Path, "cycle": cyc.ID, "start": cyc.Start.Format(time.DateOnly), "end": cyc.End.Format(time.DateOnly),
			"time_pct": tp, "earned_pct": score, "warnings": set.Warning, "goals": out, "generated_at": now.UTC(),
		})
	}
	fmt.Printf("%s  %s → %s  time %.0f%%  earned %.0f%%\n", cyc.ID, cyc.Start.Format(time.DateOnly), cyc.End.Format(time.DateOnly), tp, score)
	for _, w := range set.Warning {
		fmt.Println("  warning:", w)
	}
	for _, g := range out {
		flag := ""
		if g.Behind {
			flag = "  behind"
		}
		fmt.Printf("\n%s %s  earned %.0f%% (+%.0f%% done, unaccepted)%s\n", g.ID, g.Title, g.Earned, g.Done, flag)
		for _, it := range g.Items {
			fmt.Printf("  %-11s %5.4g  %-16s %s\n", it.State, it.Pts, it.Bead, it.Label)
		}
	}
	return nil
}
