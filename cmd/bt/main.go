package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/FoamScience/beads-tui/internal/ui"
)

var version = "dev" // set by make via -ldflags

const usage = `usage:
  bt                  open the TUI
  bt triage [--json]  print hygiene violations (JSON for agents)
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
		asJSON := len(os.Args) > 2 && os.Args[2] == "--json"
		if len(os.Args) > 3 || (len(os.Args) == 3 && !asJSON) {
			fail(fmt.Errorf("%s", usage))
		}
		if err := triage(c, asJSON); err != nil {
			fail(err)
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

func triage(c bd.Client, asJSON bool) error {
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
		return enc.Encode(map[string]any{"beads_dir": c.Dir, "host": host, "known_machines": known, "generated_at": time.Now().UTC(), "rules": rules})
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
