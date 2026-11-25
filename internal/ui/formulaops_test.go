package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
)

// TestFormulaConvertAndSeed drives t and V on formula rows against a fake bd that logs its args.
func TestFormulaConvertAndSeed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "f")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lastCall := fakeBD(t, `case "$*" in
	"mol seed gone") echo 'Error: formula "gone" not accessible' >&2; exit 1;;
	"formula convert"*) echo "✓ Converted: `+dir+`/bd-wrote.formula.toml";;
	esac`)
	jsonSrc := filepath.Join(dir, "jf.formula.json")
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	a.Update(snapshotMsg{issues: fixture(), gen: a.gen})
	a.switchTo(a.viewIndex("Workflows"))
	v := a.views[a.active].(*workflowsView)
	v.Msg(a, molDataMsg{formulas: []bd.FormulaSummary{
		{Name: "jf", Source: jsonSrc},
		{Name: "tf", Source: filepath.Join(dir, "tf.formula.toml")},
		{Name: "yf", Source: filepath.Join(dir, "yf.formula.yaml")},
		{Name: "nf"},
		{Name: "gone", Source: filepath.Join(dir, "gone.formula.toml")},
	}})
	sel := func(name string) {
		for i, it := range v.items {
			if it.formula != nil && it.formula.Name == name {
				v.cursor = i
			}
		}
	}
	key := func(k string) tea.Cmd {
		_, cmd := v.Update(a, tea.KeyPressMsg{Code: rune(k[0]), Text: k})
		return cmd
	}

	for name, want := range map[string]string{"tf": "tf is already TOML", "yf": "yf.formula.yaml is not a .formula.json file", "nf": "bd names no source file for nf"} {
		sel(name)
		if m, ok := key("t")().(flashMsg); !ok || !strings.HasPrefix(m.text, want) || a.modal != nil {
			t.Fatalf("t on %s = %#v, want %q and no modal", name, m, want)
		}
	}

	sel("jf")
	key("t")
	dst := filepath.Join(dir, "jf.formula.toml")
	if !strings.Contains(a.modal.title, "Writes ~/f/jf.formula.toml") || strings.Contains(a.modal.title, "overwritten") {
		t.Fatalf("convert confirm must name the file it writes: %q", a.modal.title)
	}
	if cmd := a.modal.onSubmit([]string{"foo"}); cmd != nil {
		t.Fatal("typed text that matches no option must not convert")
	}
	bdPath := filepath.Join(dir, "bd-wrote.formula.toml")
	for pick, want := range map[string]string{"keep": "formula convert " + jsonSrc, "delete": "formula convert " + jsonSrc + " --delete"} {
		m := a.modal.onSubmit([]string{pick})().(convertedMsg)
		if got := lastCall(); got != want || m.err != nil || m.path != bdPath {
			t.Fatalf("%s ran bd %q (want %q), msg %+v, want the path bd printed", pick, got, want, m)
		}
	}

	if err := os.WriteFile(dst, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	key("t")
	if !strings.Contains(a.modal.title, "overwritten") {
		t.Fatalf("convert confirm must warn when the TOML exists: %q", a.modal.title)
	}
	a.modal = nil

	v.details["jf"] = &bd.Formula{Name: "jf"}
	cmd := v.Msg(a, convertedMsg{name: "jf", err: errors.New("bd formula: parse error")})
	if m, ok := cmd().(flashMsg); !ok || !m.err || !strings.Contains(m.text, "convert jf: bd formula: parse error") {
		t.Fatalf("failed conversion flash = %#v", m)
	}
	if v.details["jf"] == nil || v.loading || !v.loaded {
		t.Fatal("a failed conversion must keep the formula list and its details")
	}

	cmd = v.Msg(a, convertedMsg{name: "jf", path: bdPath})
	if _, ok := v.details["jf"]; ok || !v.loading || v.loaded {
		t.Fatal("a conversion must drop the cached detail and reload the formula list")
	}
	var flashed, listed bool
	for _, c := range cmd().(tea.BatchMsg) {
		switch m := c().(type) {
		case flashMsg:
			flashed = !m.err && m.text == "converted jf to ~/f/bd-wrote.formula.toml"
		case molDataMsg:
			listed = lastCall() == "formula list --json"
		}
	}
	if !flashed || !listed {
		t.Fatalf("conversion: converted flash %v, formula list rerun %v", flashed, listed)
	}

	sel("jf")
	if m := key("V")().(flashMsg); m.err || lastCall() != "mol seed jf" {
		t.Fatalf("V ran bd %q, flash %#v", lastCall(), m)
	}
	sel("gone")
	if m := key("V")().(flashMsg); !m.err || !strings.Contains(m.text, "not accessible") {
		t.Fatalf("V on an inaccessible formula flash = %#v", m)
	}
}

func TestShortHome(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	for in, want := range map[string]string{"/home/u/f.toml": "~/f.toml", "/home/u": "~", "/home/u2/f.toml": "/home/u2/f.toml", "/srv/f": "/srv/f"} {
		if got := shortHome(in); got != want {
			t.Errorf("shortHome(%q) = %q, want %q", in, got, want)
		}
	}
	t.Setenv("HOME", "")
	if got := shortHome("/home/u/f.toml"); got != "/home/u/f.toml" {
		t.Errorf("with HOME unset shortHome = %q, want the path unchanged", got)
	}
}
