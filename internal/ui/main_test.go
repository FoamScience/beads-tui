package ui

import (
	"os"
	"slices"
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

func TestBarCells(t *testing.T) {
	for _, c := range []struct {
		counts []int
		want   []int
	}{
		{[]int{0, 0, 0, 0, 0}, []int{0, 0, 0, 0, 0}},
		{[]int{19, 0, 0, 1, 0}, []int{19, 0, 0, 1, 0}},
		{[]int{100, 1, 0, 0, 0}, []int{19, 1, 0, 0, 0}},
		{[]int{1, 1, 1, 0, 0}, []int{7, 7, 6, 0, 0}},
		{[]int{5, 0, 0, 5, 10}, []int{5, 0, 0, 5, 10}},
	} {
		got := barCells(c.counts, 20)
		if !slices.Equal(got, c.want) {
			t.Errorf("barCells(%v) = %v, want %v", c.counts, got, c.want)
		}
	}
}
