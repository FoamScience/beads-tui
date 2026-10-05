package bd

import (
	"os"
	"testing"
)

func TestSpawnWispLive(t *testing.T) {
	if os.Getenv("BT_SPAWN") == "" {
		t.Skip()
	}
	c := NewClient()
	id, err := c.Spawn("deck-sync", true, map[string]string{"ppump_sha": "bt-spawn-test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("spawned", id)
	is, err := c.Show(id)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("root title:", is.Title, "type:", is.IssueType)
	if err := c.Exec("mol", "burn", id, "--force"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Show(id); err == nil {
		t.Fatal("burned wisp still present")
	}
}
