package bd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fakeBD puts a bd on PATH that answers show from issue.json and saves update --body-file - (stdin) to it.
func fakeBD(t *testing.T, is Issue) (Client, func() Issue) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	store := filepath.Join(dir, "issue.json")
	save := func(is Issue) {
		b, _ := json.Marshal([]Issue{is})
		if err := os.WriteFile(store, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	save(is)
	script := `#!/bin/sh
case "$1" in
show) cat "` + store + `" ;;
update) python3 -I -c 'import json,sys; s=sys.argv[1]; d=json.load(open(s)); d[0]["description"]=sys.stdin.read(); json.dump(d,open(s,"w"))' "` + store + `" ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "bd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	load := func() Issue {
		var out []Issue
		b, _ := os.ReadFile(store)
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		return out[0]
	}
	return Client{Dir: filepath.Join(dir, ".beads")}, load
}

func TestLedgerCommit(t *testing.T) {
	ledger := Issue{ID: "tasks-x.1", Status: "pinned", Labels: []string{"live"}, Description: "v1"}
	c, load := fakeBD(t, ledger)

	a, err := c.LedgerCheckout(ledger.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.LedgerCheckout(ledger.ID)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(a, []byte("v2 from a"), 0o644)
	os.WriteFile(b, []byte("v2 from b"), 0o644)
	if err := c.LedgerCommit(a); err != nil {
		t.Fatal(err)
	}
	if got := load().Description; got != "v2 from a" {
		t.Fatalf("first commit not written: %q", got)
	}
	if err := c.LedgerCommit(b); !errors.Is(err, ErrLedgerStale) {
		t.Fatalf("second commit on a stale checkout: %v, want ErrLedgerStale", err)
	}
	if got := load().Description; got != "v2 from a" {
		t.Fatalf("stale commit overwrote the ledger: %q", got)
	}
	if got, _ := os.ReadFile(b); string(got) != "v2 from b" {
		t.Fatalf("stale commit lost the edit: %q", got)
	}
	if err := c.LedgerCommit(b); err == nil || errors.Is(err, ErrLedgerStale) {
		t.Fatalf("stale checkout committed again: %v, want not a checkout", err)
	}

	ledger.Status = "open"
	c, _ = fakeBD(t, ledger)
	if _, err := c.LedgerCheckout(ledger.ID); err == nil {
		t.Fatal("checked out a bead that is not a live ledger")
	}
}
