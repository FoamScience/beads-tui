package ui

import (
	"os"
	"testing"
)

// TestMain points config and cache at a scratch directory, so no test can overwrite the
// developer's real ~/.config/bt/state.json or snapshot cache.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "bt-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CONFIG_HOME", dir)
	os.Setenv("XDG_CACHE_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
