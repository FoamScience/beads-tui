package main

import (
	"os"
	"testing"
)

func TestTriageArgs(t *testing.T) {
	host, _ := os.Hostname()
	for _, c := range []struct {
		args    []string
		json    bool
		machine string
		bad     bool
	}{
		{nil, false, "all", false},
		{[]string{"--json"}, true, "all", false},
		{[]string{"--machine"}, false, host, false},
		{[]string{"--machine", "--json"}, true, host, false},
		{[]string{"--json", "--machine", "box"}, true, "box", false},
		{[]string{"--machine", "all"}, false, "all", false},
		{[]string{"--bogus"}, false, "", true},
	} {
		j, m, err := triageArgs(c.args)
		if (err != nil) != c.bad || j != c.json || m != c.machine {
			t.Errorf("%v: got %v %q %v", c.args, j, m, err)
		}
	}
}
