package dispatch

import (
	"cmp"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/FoamScience/beads-tui/internal/bd"
)

const deciderBatch = 10

// Verdict is the decider's lane for one bead: a configured lane, or a lane to open when New.
type Verdict struct {
	ID, Lane, Why string
	New           bool
	About         string // the new lane's description
}

// Unrouted lists ready beads dispatch.toml allows that no rule routes and tried has not seen.
func Unrouted(c *Config, s *bd.Snapshot, a Allow, tried map[string]bool) []*bd.Issue {
	if c.Decider == nil || c.Decider.Model == "" {
		return nil
	}
	var out []*bd.Issue
	for _, is := range s.Issues {
		if tried[is.ID] || StateOf(s, is) != Queued || len(s.Children[is.ID]) > 0 || Route(c, s, is) != "" {
			continue
		}
		if _, ok := c.Lanes[is.Assignee]; ok || !a.Permits(s, is, "") {
			continue
		}
		if out = append(out, is); len(out) == deciderBatch {
			break
		}
	}
	return out
}

func DeciderPrompt(c *Config, s *bd.Snapshot, beads []*bd.Issue) string {
	var b strings.Builder
	b.WriteString("Route each task to the one lane that should work it, or none when no lane fits.\n\nLanes:\n")
	for _, name := range c.Names() {
		l := c.Lanes[name]
		fmt.Fprintf(&b, "- %s: %s", name, cmp.Or(l.About, "(no description)"))
		if len(l.Takes.Labels) > 0 {
			fmt.Fprintf(&b, " [takes labels %s]", strings.Join(l.Takes.Labels, ", "))
		}
		b.WriteString("\n")
	}
	b.WriteString("\nTasks:\n")
	for _, is := range beads {
		fmt.Fprintf(&b, "- id %s: %s", is.ID, is.Title)
		if e := s.ByID[is.Parent]; e != nil {
			fmt.Fprintf(&b, " (under %s)", e.Title)
		}
		if len(is.Labels) > 0 {
			fmt.Fprintf(&b, " labels %s", strings.Join(is.Labels, ", "))
		}
		if d := strings.Join(strings.Fields(is.Description), " "); d != "" {
			fmt.Fprintf(&b, " -- %.300s", d)
		}
		b.WriteString("\n")
	}
	b.WriteString("\nAnswer with one line per task and nothing else: <id><TAB><short reason><TAB><lane or none>\n" +
		"Decide the lane from the reason: when the reason says no lane fits, the lane is none.\n" +
		"A task goes to a listed lane when it falls within that lane's description; never stretch a lane to cover unrelated work.\n")
	if c.Auto != nil && c.AutoLanes() < c.Auto.MaxLanes {
		fmt.Fprintf(&b, "When no listed lane fits, open a new lane instead of answering none (at most %d new lanes overall): "+
			"answer <id><TAB><short reason><TAB>new:<name><TAB><one line on what the lane works on>, "+
			"name in lowercase-with-dashes. Give tasks that belong together the same new name.\n", c.Auto.MaxLanes-c.AutoLanes())
	}
	return b.String()
}

// ParseVerdicts keeps only lines for asked beads that name a configured lane.
func ParseVerdicts(c *Config, asked []*bd.Issue, out string) []Verdict {
	want := map[string]bool{}
	for _, is := range asked {
		want[is.ID] = true
	}
	var vs []Verdict
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), "\t")
		if len(f) < 3 || !want[f[0]] {
			continue
		}
		v := Verdict{ID: f[0], Lane: f[2], Why: f[1]}
		if name, ok := strings.CutPrefix(f[2], "new:"); ok {
			if len(f) != 4 || c.Auto == nil {
				continue
			}
			v.Lane, v.New, v.About = name, c.Lanes[name] == nil, f[3]
		} else if len(f) != 3 || c.Lanes[f[2]] == nil {
			continue
		}
		want[f[0]] = false
		vs = append(vs, v)
	}
	return vs
}

// askDecider runs the model with no tools, hooks, MCP servers or saved session.
func askDecider(model, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "-p", "--model", model, "--tools", "", "--no-session-persistence",
		"--strict-mcp-config", "--setting-sources", "")
	cmd.Stdin = strings.NewReader(prompt)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("decider %s: %w", model, err)
	}
	return string(out), nil
}
