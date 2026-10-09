package ui

import "testing"

func newTestPager(text string, h int) *pager {
	p := &pager{focused: true}
	p.SetSize(40, h)
	p.SetContent(text, true)
	return p
}

func (p *pager) keys(ks ...string) *pager {
	for _, k := range ks {
		p.Key(k)
	}
	return p
}

func TestPagerMotions(t *testing.T) {
	text := "foo.bar baz\n  indented line\n\nnext para here\nlast"
	cases := []struct {
		keys     []string
		row, col int
	}{
		{[]string{"w"}, 0, 3},                     // foo -> .
		{[]string{"w", "w"}, 0, 4},                // . -> bar
		{[]string{"W"}, 0, 8},                     // WORD skips foo.bar
		{[]string{"e"}, 0, 2},                     // end of foo
		{[]string{"E"}, 0, 6},                     // end of foo.bar
		{[]string{"$", "w"}, 1, 2},                // w crosses to the next line's first word
		{[]string{"j", "b"}, 0, 8},                // b back over the line break to baz
		{[]string{"j", "^"}, 1, 2},                // first non-blank
		{[]string{"j", "$", "0"}, 1, 0},           // start of line
		{[]string{"$"}, 0, 10},                    // end of line
		{[]string{"}"}, 2, 0},                     // next blank line
		{[]string{"}", "}"}, 4, 0},                // past the last paragraph: last line
		{[]string{"G", "{"}, 2, 0},                // previous blank line
		{[]string{"G"}, 4, 0},                     // bottom
		{[]string{"G", "g", "g"}, 0, 0},           // top
		{[]string{"4", "G"}, 3, 0},                // count + G goes to line 4
		{[]string{"3", "j"}, 3, 0},                // count + j
		{[]string{"2", "w"}, 0, 4},                // count + w
		{[]string{"5", "g", "g"}, 4, 0},           // count + gg
		{[]string{"j", "j", "j", "l", "l"}, 3, 2}, // l within a line
	}
	for _, c := range cases {
		p := newTestPager(text, 10).keys(c.keys...)
		if p.row != c.row || p.col != c.col {
			t.Errorf("%v: at %d:%d, want %d:%d", c.keys, p.row, p.col, c.row, c.col)
		}
	}
}

func TestPagerSearchAndScreen(t *testing.T) {
	p := newTestPager("alpha\nbeta Alpha\ngamma\nalpha again", 2)
	if !p.Search("ALPHA") || p.row != 1 || p.col != 5 {
		t.Fatalf("search from the top should find the next match, at %d:%d", p.row, p.col)
	}
	p.keys("n")
	if p.row != 3 || p.col != 0 {
		t.Fatalf("n: at %d:%d", p.row, p.col)
	}
	p.keys("n")
	if p.row != 0 {
		t.Fatalf("n should wrap to the top, at row %d", p.row)
	}
	p.keys("N")
	if p.row != 3 {
		t.Fatalf("N should wrap backwards, at row %d", p.row)
	}
	if p.off != 2 {
		t.Fatalf("cursor on the last line should scroll the 2-line screen, off=%d", p.off)
	}
	p.keys("H")
	if p.row != 2 {
		t.Fatalf("H: row %d", p.row)
	}
	if p.Search("nothing") {
		t.Fatal("a missing pattern must report no match")
	}
}
