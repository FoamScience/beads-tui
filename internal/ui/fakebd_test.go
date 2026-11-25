package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBD puts a bd on PATH that logs each call's args and then runs the shell snippet body, and
// returns a func reading the last logged call.
func fakeBD(t *testing.T, body string) func() string {
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "bd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return func() string {
		b, _ := os.ReadFile(log)
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		return lines[len(lines)-1]
	}
}
