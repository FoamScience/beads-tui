// Package bd reads the beads DB through `bd --json` and writes through bd subcommands.
package bd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Dependency struct {
	IssueID     string    `json:"issue_id"`
	DependsOnID string    `json:"depends_on_id"`
	Type        string    `json:"type"`
	CreatedAt   time.Time `json:"created_at"`
}

type Ref struct {
	Repo    string `json:"repo"`
	File    string `json:"file"`
	Symbol  string `json:"symbol"`
	Pattern string `json:"pattern"`
	Line    int    `json:"line"`
}

type Issue struct {
	ID                 string          `json:"id"`
	Title              string          `json:"title"`
	Description        string          `json:"description"`
	Design             string          `json:"design"`
	AcceptanceCriteria string          `json:"acceptance_criteria"`
	Notes              string          `json:"notes"`
	Status             string          `json:"status"`
	Priority           int             `json:"priority"`
	IssueType          string          `json:"issue_type"`
	Owner              string          `json:"owner"`
	Assignee           string          `json:"assignee"`
	CreatedBy          string          `json:"created_by"`
	Labels             []string        `json:"labels"`
	Parent             string          `json:"parent"`
	ExternalRef        string          `json:"external_ref"`
	EstimatedMinutes   int             `json:"estimated_minutes"`
	CloseReason        string          `json:"close_reason"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	StartedAt          *time.Time      `json:"started_at"`
	ClosedAt           *time.Time      `json:"closed_at"`
	DeferUntil         *time.Time      `json:"defer_until"`
	Dependencies       []Dependency    `json:"dependencies"`
	DependencyCount    int             `json:"dependency_count"`
	DependentCount     int             `json:"dependent_count"`
	CommentCount       int             `json:"comment_count"`
	Metadata           json.RawMessage `json:"metadata"`
}

type Comment struct {
	ID        string    `json:"id"`
	Author    string    `json:"author"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

func (i *Issue) Refs() []Ref {
	var m struct {
		Refs []Ref `json:"refs"`
	}
	if len(i.Metadata) == 0 || json.Unmarshal(i.Metadata, &m) != nil {
		return nil
	}
	return m.Refs
}

func (i *Issue) HasLabel(l string) bool {
	for _, x := range i.Labels {
		if x == l {
			return true
		}
	}
	return false
}

func (i *Issue) LabelWithPrefix(p string) string {
	for _, x := range i.Labels {
		if strings.HasPrefix(x, p) {
			return strings.TrimPrefix(x, p)
		}
	}
	return ""
}

func (i *Issue) Closed() bool { return i.Status == "closed" }

type Client struct {
	Dir string // BEADS_DIR
}

func NewClient() Client {
	dir := os.Getenv("BEADS_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, "tasks", ".beads")
	}
	return Client{Dir: dir}
}

// Run executes bd and returns stdout; the error carries bd's last stderr line.
func (c Client) Run(ctx context.Context, args ...string) ([]byte, error) {
	return c.run(ctx, "", args...)
}

// ExecInput runs a write that reads its payload from stdin (e.g. update --body-file -).
func (c Client) ExecInput(input string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, err := c.run(ctx, input, args...)
	return err
}

func (c Client) run(ctx context.Context, input string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "bd", args...)
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	cmd.Env = append(os.Environ(), "BEADS_DIR="+c.Dir)
	cmd.Dir = filepath.Dir(c.Dir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := lastLine(stderr.String())
		if msg == "" {
			msg = lastLine(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("bd %s: %s", args[0], msg)
	}
	return stdout.Bytes(), nil
}

func (c Client) Exec(args ...string) error {
	_, err := c.Output(args...)
	return err
}

// Output runs a bd command with the default write timeout.
func (c Client) Output(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return c.Run(ctx, args...)
}

func (c Client) runJSON(v any, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := c.Run(ctx, append(args, "--json")...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("bd %s: bad json: %w", args[0], err)
	}
	return nil
}

func (c Client) List() ([]Issue, error) {
	var out []Issue
	err := c.runJSON(&out, "list", "--all", "--limit", "0")
	return out, err
}

func (c Client) Show(id string) (Issue, error) {
	var out []Issue
	if err := c.runJSON(&out, "show", id); err != nil {
		return Issue{}, err
	}
	if len(out) == 0 {
		return Issue{}, errors.New("bd show: no issue " + id)
	}
	return out[0], nil
}

func (c Client) Comments(id string) ([]Comment, error) {
	var out []Comment
	err := c.runJSON(&out, "comments", id)
	return out, err
}

func (c Client) Ready() ([]Issue, error) {
	var out []Issue
	err := c.runJSON(&out, "ready", "--limit", "0")
	return out, err
}

// ChangeStamp returns the Dolt manifest contents, which change on every commit (local write or pull).
// The mtime is useless here: even read-only bd calls rewrite the file.
func (c Client) ChangeStamp() string {
	matches, _ := filepath.Glob(filepath.Join(c.Dir, "embeddeddolt", "*", ".dolt", "noms", "manifest"))
	var b strings.Builder
	for _, m := range matches {
		if data, err := os.ReadFile(m); err == nil {
			b.Write(data)
		}
	}
	return b.String()
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
