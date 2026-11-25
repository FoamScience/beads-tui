package bd

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const wispDemo = `formula = "bt-wisp-demo"
type = "workflow"
version = 1

[[steps]]
id = "a"
title = "step a"
type = "task"

[[steps]]
id = "b"
title = "step b"
type = "task"
needs = ["a"]
`

// TestWispLifecycleLive runs squash, burn and the gc preview and delete on real wisp roots, in a
// database it creates under its own temp dir, so it can never touch ~/tasks.
func TestWispLifecycleLive(t *testing.T) {
	if os.Getenv("BT_WISP_LIVE") == "" {
		t.Skip("live bd test: set BT_WISP_LIVE=1 to run it in a fresh temp database")
	}
	tmp := t.TempDir()
	cmd := exec.Command("bd", "init", "--prefix", "wt", "--non-interactive", "--quiet")
	cmd.Dir = tmp
	cmd.Env = append(slices.DeleteFunc(os.Environ(), func(e string) bool { return strings.HasPrefix(e, "BEADS_DIR=") }), "BD_NON_INTERACTIVE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bd init: %v\n%s", err, out)
	}
	dir := filepath.Join(tmp, ".beads")
	c := Client{Dir: dir}
	if err := os.MkdirAll(filepath.Join(dir, "formulas"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "formulas", "bt-wisp-demo.formula.toml"), []byte(wispDemo), 0o644); err != nil {
		t.Fatal(err)
	}
	spawn := func() string {
		id, err := c.Spawn("bt-wisp-demo", true, nil)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	snap := func() *Snapshot {
		issues, err := c.List()
		if err != nil {
			t.Fatal(err)
		}
		return NewSnapshot(issues)
	}
	sq, bn, gc := spawn(), spawn(), spawn()
	s := snap()
	for _, id := range []string{sq, bn, gc} {
		if s.ByID[id] != nil || s.Ephemeral.ByID[id] == nil {
			t.Fatalf("wisp root %s not in the ephemeral snapshot", id)
		}
	}

	if err := c.Exec("mol", "squash", sq); err != nil {
		t.Fatal(err)
	}
	if err := c.Exec("mol", "burn", bn, "--force"); err != nil {
		t.Fatal(err)
	}
	s = snap()
	root := s.ByID[sq]
	if root == nil || !root.Closed() || s.Ephemeral.ByID[sq] != nil {
		t.Fatalf("squashed root %s should be persistent and closed: %+v", sq, root)
	}
	if kids := s.Children[sq]; len(kids) != 1 || kids[0].Ephemeral {
		t.Fatalf("squashed root %s should hold one persistent digest, has %d children", sq, len(kids))
	}
	if s.ByID[bn] != nil || s.Ephemeral.ByID[bn] != nil || len(s.Ephemeral.Children[bn]) > 0 {
		t.Fatalf("burned root %s or its steps still present", bn)
	}

	args := []string{"close", "--force"}
	for _, st := range s.Ephemeral.Children[gc] {
		args = append(args, st.ID)
	}
	if err := c.Exec(args...); err != nil {
		t.Fatal(err)
	}
	if !snap().Ephemeral.ByID[gc].Closed() {
		if err := c.Exec("close", gc); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := c.WispGCPreview("--closed")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{gc}
	for _, st := range s.Ephemeral.Children[gc] {
		want = append(want, st.ID)
	}
	slices.Sort(want)
	if !slices.Equal(ids, want) {
		t.Fatalf("gc --closed preview %v, want the closed wisp and its steps %v", ids, want)
	}
	if err := c.Exec(append([]string{"delete", "--force"}, ids...)...); err != nil {
		t.Fatal(err)
	}
	if s = snap(); len(s.Ephemeral.Issues) != 0 {
		t.Fatalf("deleting the previewed ids left wisp rows %v", s.Ephemeral.Issues)
	}

	// An open wisp is abandoned at age 0, except the step still waiting on its sibling.
	ab := spawn()
	want = []string{ab}
	for _, st := range snap().Ephemeral.Children[ab] {
		if !slices.ContainsFunc(st.Dependencies, func(d Dependency) bool { return d.Type == "blocks" }) {
			want = append(want, st.ID)
		}
	}
	slices.Sort(want)
	if ids, err = c.WispGCPreview("--age", "0s"); err != nil || !slices.Equal(ids, want) {
		t.Fatalf("abandoned preview %v (%v), want root and unblocked step %v", ids, err, want)
	}
}
