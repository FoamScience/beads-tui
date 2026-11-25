package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
)

type convertedMsg struct {
	name, path string
	err        error
}

// shortHome writes a path under $HOME as ~/…
func shortHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if rest, ok := strings.CutPrefix(p, home); ok && (rest == "" || rest[0] == filepath.Separator) {
		return "~" + rest
	}
	return p
}

// convertModal asks before bd formula convert writes <name>.formula.toml next to a .formula.json,
// the only input bd accepts. bd loads the TOML in place of the JSON, while formula list keeps
// naming the JSON, and convert overwrites an existing TOML without asking.
func (v *workflowsView) convertModal(a *App, f *bd.FormulaSummary) tea.Cmd {
	src := f.Source
	switch {
	case src == "":
		return flash("bd names no source file for " + f.Name)
	case strings.HasSuffix(src, ".formula.toml"):
		return flash(f.Name + " is already TOML")
	case !strings.HasSuffix(src, ".formula.json"):
		return flash(filepath.Base(src) + " is not a .formula.json file, which is all bd formula convert reads")
	}
	dst := strings.TrimSuffix(src, ".formula.json") + ".formula.toml"
	title := "Convert " + f.Name + " to TOML?\n\nWrites " + shortHome(dst)
	if _, err := os.Stat(dst); err == nil {
		title += "\n\n" + sWarn.Render("That file exists and is overwritten: bd already loads it in place of the JSON.")
	}
	base := filepath.Base(src)
	a.modal = newPicker(title, []option{
		{"keep", "convert, keep " + base},
		{"delete", "convert, then delete " + base},
	}, func(pick string) tea.Cmd {
		if pick != "keep" && pick != "delete" {
			return nil
		}
		args := []string{"formula", "convert", src}
		if pick == "delete" {
			args = append(args, "--delete")
		}
		c := a.client
		return func() tea.Msg {
			out, err := c.Output(args...)
			path := dst
			for _, l := range strings.Split(string(out), "\n") {
				if _, p, ok := strings.Cut(l, "Converted: "); ok {
					path = strings.TrimSpace(p)
				}
			}
			return convertedMsg{f.Name, path, err}
		}
	})
	return nil
}

// seed runs bd mol seed: the formula resolves through bd's search paths and cooks.
func (v *workflowsView) seed(a *App, name string) tea.Cmd {
	c := a.client
	return func() tea.Msg {
		if _, err := c.Output("mol", "seed", name); err != nil {
			return flashMsg{text: "seed " + name + ": " + err.Error(), err: true}
		}
		return flashMsg{text: "seed " + name + ": accessible"}
	}
}

func (v *workflowsView) converted(a *App, m convertedMsg) tea.Cmd {
	if m.err != nil {
		return flashErr(fmt.Errorf("convert %s: %w", m.name, m.err))
	}
	return tea.Batch(v.reloadFormulas(a, m.name), flash("converted "+m.name+" to "+shortHome(m.path)))
}

// reloadFormulas drops the named formulas' cached details and lists the formulas again.
func (v *workflowsView) reloadFormulas(a *App, names ...string) tea.Cmd {
	for _, n := range names {
		delete(v.details, n)
	}
	v.loaded = false
	return v.load(a)
}
