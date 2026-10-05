package ui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/elwardi/beads-tui/internal/bd"
)

func TestDump(t *testing.T) {
	if os.Getenv("BT_DUMP") == "" {
		t.Skip()
	}
	c := bd.NewClient()
	is, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	a := New(c)
	w, _ := strconv.Atoi(os.Getenv("BT_W"))
	h, _ := strconv.Atoi(os.Getenv("BT_H"))
	a.Update(tea.WindowSizeMsg{Width: w, Height: h})
	a.Update(snapshotMsg{issues: is})
	if r := os.Getenv("BT_ROOT"); r != "" {
		a.views[5].(*graphView).showClosed = os.Getenv("BT_CLOSED") != ""
		a.views[5].(*graphView).setRoot(a, a.snap.ByID[r])
		a.active = 5
	}
	for _, k := range os.Getenv("BT_KEYS") {
		if k == '\r' {
			drive(a, tea.KeyPressMsg{Code: tea.KeyEnter})
			continue
		}
		drive(a, tea.KeyPressMsg{Code: k, Text: string(k)})
	}
	fmt.Println(a.render())
}

// drive feeds msg into the app and runs resulting commands synchronously (ticks dropped).
func drive(a *App, msg tea.Msg) {
	_, cmd := a.Update(msg)
	run(a, cmd)
}

func run(a *App, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch m := cmd().(type) {
	case nil, tickMsg:
	case tea.BatchMsg:
		for _, c := range m {
			run(a, c)
		}
	default:
		drive(a, m)
	}
}

func TestWriteNote(t *testing.T) {
	id := os.Getenv("BT_NOTE_ID")
	if id == "" {
		t.Skip()
	}
	c := bd.NewClient()
	is, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	a := New(c)
	drive(a, tea.WindowSizeMsg{Width: 120, Height: 40})
	drive(a, snapshotMsg{issues: is})
	a.override = a.snap.ByID[id]
	drive(a, tea.KeyPressMsg{Code: 'n', Text: "n"})
	for _, r := range "smoke: note written from bt" {
		drive(a, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	drive(a, tea.KeyPressMsg{Code: tea.KeyEnter})
	if a.flashErr {
		t.Fatal(a.flash)
	}
	got, err := c.Show(id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Notes, "smoke: note written from bt") {
		t.Fatalf("note missing: %q", got.Notes)
	}
	t.Log(a.flash)
}
