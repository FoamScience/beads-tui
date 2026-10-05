package bd

import (
	"encoding/json"
	"fmt"
	"strings"
)

type FormulaSummary struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Steps       int    `json:"steps"`
	Vars        int    `json:"vars"`
}

type FormulaVar struct {
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Default     string `json:"default"`
}

type FormulaStep struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Type        string   `json:"type"`
	Priority    int      `json:"priority"`
	Labels      []string `json:"labels"`
	Needs       []string `json:"needs"`
	DependsOn   []string `json:"depends_on"`
	Description string   `json:"description"`
}

type Formula struct {
	Name        string                `json:"formula"`
	Description string                `json:"description"`
	Type        string                `json:"type"`
	Phase       string                `json:"phase"`
	Source      string                `json:"source"`
	Vars        map[string]FormulaVar `json:"vars"`
	Steps       []FormulaStep         `json:"steps"`
}

type Wisp struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type StaleMolecule struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	ClosedChildren int    `json:"closed_children"`
	TotalChildren  int    `json:"total_children"`
}

func (c Client) Formulas() ([]FormulaSummary, error) {
	var out []FormulaSummary
	err := c.runJSON(&out, "formula", "list")
	return out, err
}

func (c Client) Formula(name string) (Formula, error) {
	var f Formula
	err := c.runJSON(&f, "formula", "show", name)
	return f, err
}

func (c Client) Wisps() ([]Wisp, error) {
	var out struct {
		Wisps []Wisp `json:"wisps"`
	}
	err := c.runJSON(&out, "mol", "wisp", "list")
	return out.Wisps, err
}

func (c Client) Stale() ([]StaleMolecule, error) {
	var out struct {
		Stale []StaleMolecule `json:"stale_molecules"`
	}
	err := c.runJSON(&out, "mol", "stale")
	return out.Stale, err
}

// Snapshot turns formula steps into issues so the graph view can lay them out.
func (f Formula) Snapshot() *Snapshot {
	root := Issue{ID: f.Name, Title: f.Name, IssueType: "epic", Status: "open"}
	issues := []Issue{root}
	prefix := commonTitlePrefix(f.Steps)
	for _, st := range f.Steps {
		is := Issue{ID: st.ID, Title: strings.TrimPrefix(st.Title, prefix), IssueType: st.Type, Status: "open", Priority: st.Priority, Labels: st.Labels, Parent: f.Name, Description: st.Description}
		for _, n := range append(st.Needs, st.DependsOn...) {
			is.Dependencies = append(is.Dependencies, Dependency{IssueID: st.ID, DependsOnID: n, Type: "blocks"})
		}
		issues = append(issues, is)
	}
	return NewSnapshot(issues)
}

// Spawn pours (or wisps) a formula and returns the new root id.
func (c Client) Spawn(name string, wisp bool, vars map[string]string) (string, error) {
	args := []string{"mol", "pour", name}
	if wisp {
		args = []string{"mol", "wisp", "create", name}
	}
	for k, v := range vars {
		args = append(args, "--var", k+"="+v)
	}
	out, err := c.Output(append(args, "--json")...)
	if err != nil {
		return "", err
	}
	var res struct {
		NewEpicID string `json:"new_epic_id"`
		RootID    string `json:"root_id"`
	}
	if i := strings.IndexByte(string(out), '{'); i >= 0 {
		_ = json.Unmarshal(out[i:], &res)
	}
	if res.NewEpicID != "" {
		return res.NewEpicID, nil
	}
	if res.RootID != "" {
		return res.RootID, nil
	}
	return "", fmt.Errorf("spawned %s but could not read the new root id", name)
}

// DryRun returns bd's preview of what a pour or wisp would create.
func (c Client) DryRun(name string, wisp bool, vars map[string]string) (string, error) {
	args := []string{"mol", "pour", name, "--dry-run"}
	if wisp {
		args = []string{"mol", "wisp", "create", name, "--dry-run"}
	}
	for k, v := range vars {
		args = append(args, "--var", k+"="+v)
	}
	out, err := c.Output(args...)
	return string(out), err
}

// commonTitlePrefix finds a shared "name {{var}}: " lead-in so previews show what differs.
func commonTitlePrefix(steps []FormulaStep) string {
	if len(steps) < 2 {
		return ""
	}
	p := steps[0].Title
	for _, st := range steps[1:] {
		for !strings.HasPrefix(st.Title, p) {
			p = p[:len(p)-1]
		}
	}
	if i := strings.LastIndex(p, ": "); i >= 0 {
		return p[:i+2]
	}
	return ""
}
