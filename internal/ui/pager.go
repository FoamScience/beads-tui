package ui

import (
	"strconv"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// pager is a read-only text view with a vim-style cursor: counts, hjkl, word and WORD motions,
// line ends, paragraphs, screen jumps, gg/G, zz/zt/zb and /-search with n/N.
type pager struct {
	lines   []string // rendered, with styles
	plain   [][]rune // the same lines without styles, for motions and search
	row     int
	col     int // rune index in plain[row]
	want    int // column to return to when moving vertically ($ sets it past the end)
	off     int
	w, h    int
	count   string
	pending string // first key of a two-key command (g, z)
	search  string
	focused bool
}

var sCursor = lipgloss.NewStyle().Reverse(true)

func (p *pager) SetSize(w, h int) { p.w, p.h = w, max(h, 1); p.scroll() }

// SetContent replaces the text and keeps the cursor in range; reset puts it back at the top.
func (p *pager) SetContent(s string, reset bool) {
	p.lines = strings.Split(strings.TrimRight(s, "\n"), "\n")
	p.plain = make([][]rune, len(p.lines))
	for i, l := range p.lines {
		p.plain[i] = []rune(ansi.Strip(l))
	}
	if reset {
		p.row, p.col, p.want, p.off = 0, 0, 0, 0
	}
	p.row = clamp(p.row, 0, len(p.lines)-1)
	p.col = clamp(p.col, 0, max(len(p.plain[p.row])-1, 0))
	p.scroll()
}

func (p *pager) View() string {
	var b strings.Builder
	for i := p.off; i < min(p.off+p.h, len(p.lines)); i++ {
		if i > p.off {
			b.WriteByte('\n')
		}
		line := p.lines[i]
		if i == p.row && p.focused {
			// draw the cursor over one cell, keeping the line's own styling around it
			c := 0
			if p.col > 0 {
				c = ansi.StringWidth(string(p.plain[i][:min(p.col, len(p.plain[i]))]))
			}
			ch := " "
			if p.col < len(p.plain[i]) {
				ch = string(p.plain[i][p.col])
			}
			cw := max(ansi.StringWidth(ch), 1)
			line = ansi.Cut(line, 0, c) + sCursor.Render(ch) + ansi.Cut(line, c+cw, ansi.StringWidth(line))
		}
		b.WriteString(line)
	}
	return b.String()
}

// GotoLine moves the cursor to the start of a line and scrolls it into view.
func (p *pager) GotoLine(row int) {
	p.row, p.col, p.want = clamp(row, 0, len(p.lines)-1), 0, 0
	p.scroll()
}

func (p *pager) scroll() {
	if len(p.lines) == 0 {
		return
	}
	if p.row < p.off {
		p.off = p.row
	}
	if p.row >= p.off+p.h {
		p.off = p.row - p.h + 1
	}
	p.off = clamp(p.off, 0, max(len(p.lines)-p.h, 0))
}

func (p *pager) n() int {
	n, err := strconv.Atoi(p.count)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// Key applies one key; it reports whether the key was a pager key.
func (p *pager) Key(k string) bool {
	if len(p.lines) == 0 {
		return false
	}
	if p.pending != "" {
		seq := p.pending + k
		p.pending = ""
		defer func() { p.count = "" }()
		switch seq {
		case "gg":
			p.GotoLine(p.n() - 1)
			if p.count == "" {
				p.GotoLine(0)
			}
		case "zz":
			p.off = clamp(p.row-p.h/2, 0, max(len(p.lines)-p.h, 0))
		case "zt":
			p.off = clamp(p.row, 0, max(len(p.lines)-p.h, 0))
		case "zb":
			p.off = clamp(p.row-p.h+1, 0, max(len(p.lines)-p.h, 0))
		}
		return true
	}
	if len(k) == 1 && k[0] >= '0' && k[0] <= '9' && (k != "0" || p.count != "") {
		p.count += k
		return true
	}
	n := p.n()
	handled := true
	switch k {
	case "g", "z":
		p.pending = k
		return true
	case "j", "down":
		p.vertical(n)
	case "k", "up":
		p.vertical(-n)
	case "h", "left":
		p.col = max(p.col-n, 0)
		p.want = p.col
	case "l", "right":
		p.col = min(p.col+n, max(len(p.plain[p.row])-1, 0))
		p.want = p.col
	case "0", "home":
		p.col, p.want = 0, 0
	case "^":
		p.col = firstNonBlank(p.plain[p.row])
		p.want = p.col
	case "$", "end":
		p.col = max(len(p.plain[p.row])-1, 0)
		p.want = 1 << 30
	case "G":
		if p.count != "" {
			p.GotoLine(n - 1)
		} else {
			p.GotoLine(len(p.lines) - 1)
		}
	case "w", "W", "b", "B", "e", "E":
		for range n {
			p.word(k)
		}
	case "}":
		for range n {
			p.paragraph(1)
		}
	case "{":
		for range n {
			p.paragraph(-1)
		}
	case "H":
		p.row = p.off
		p.col = firstNonBlank(p.plain[p.row])
	case "M":
		p.row = min(p.off+p.h/2, len(p.lines)-1)
		p.col = firstNonBlank(p.plain[p.row])
	case "L":
		p.row = min(p.off+p.h-1, len(p.lines)-1)
		p.col = firstNonBlank(p.plain[p.row])
	case "ctrl+d", "pgdown", "space":
		p.page(p.h / 2 * n)
	case "ctrl+u", "pgup":
		p.page(-p.h / 2 * n)
	case "ctrl+f":
		p.page(p.h * n)
	case "ctrl+b":
		p.page(-p.h * n)
	case "n":
		p.find(1, n)
	case "N":
		p.find(-1, n)
	default:
		handled = false
	}
	p.count = ""
	p.scroll()
	return handled
}

func (p *pager) vertical(d int) {
	p.row = clamp(p.row+d, 0, len(p.lines)-1)
	p.col = min(p.want, max(len(p.plain[p.row])-1, 0))
}

func (p *pager) page(d int) {
	p.off = clamp(p.off+d, 0, max(len(p.lines)-p.h, 0))
	p.vertical(d)
}

func firstNonBlank(l []rune) int {
	for i, r := range l {
		if !unicode.IsSpace(r) {
			return i
		}
	}
	return 0
}

// class sorts runes for word motions: 0 blank, 1 word character, 2 punctuation.
// For WORD motions (big) anything non-blank is one class.
func class(r rune, big bool) int {
	switch {
	case unicode.IsSpace(r):
		return 0
	case big || unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_':
		return 1
	}
	return 2
}

// at returns the rune at a position, treating line ends as blanks so motions cross lines.
func (p *pager) at(row, col int) rune {
	if col >= len(p.plain[row]) {
		return ' '
	}
	return p.plain[row][col]
}

func (p *pager) next(row, col int) (int, int, bool) {
	if col+1 < len(p.plain[row]) {
		return row, col + 1, true
	}
	if col < len(p.plain[row]) && len(p.plain[row]) > 0 {
		return row, len(p.plain[row]), true // the virtual blank at the end of the line
	}
	if row+1 < len(p.lines) {
		return row + 1, 0, true
	}
	return row, col, false
}

func (p *pager) prev(row, col int) (int, int, bool) {
	if col > 0 {
		return row, col - 1, true
	}
	if row > 0 {
		return row - 1, len(p.plain[row-1]), true
	}
	return row, col, false
}

func (p *pager) word(k string) {
	big := k == "W" || k == "B" || k == "E"
	r, c := p.row, p.col
	cls := func(r, c int) int { return class(p.at(r, c), big) }
	ok := true
	switch strings.ToLower(k) {
	case "w":
		start := cls(r, c)
		for ok && start != 0 && cls(r, c) == start {
			r, c, ok = p.next(r, c)
		}
		for ok && cls(r, c) == 0 {
			r, c, ok = p.next(r, c)
		}
	case "e":
		r, c, ok = p.next(r, c)
		for ok && cls(r, c) == 0 {
			r, c, ok = p.next(r, c)
		}
		cur := cls(r, c)
		for ok {
			nr, nc, more := p.next(r, c)
			if !more || cls(nr, nc) != cur {
				break
			}
			r, c = nr, nc
		}
	case "b":
		r, c, ok = p.prev(r, c)
		for ok && cls(r, c) == 0 {
			r, c, ok = p.prev(r, c)
		}
		cur := cls(r, c)
		for ok {
			pr, pc, more := p.prev(r, c)
			if !more || cls(pr, pc) != cur {
				break
			}
			r, c = pr, pc
		}
	}
	p.row, p.col = r, min(c, max(len(p.plain[r])-1, 0))
	p.want = p.col
}

func (p *pager) blank(row int) bool { return strings.TrimSpace(string(p.plain[row])) == "" }

// paragraph moves to the next or previous blank line after a run of text, like { and }.
func (p *pager) paragraph(d int) {
	r := p.row
	for r+d >= 0 && r+d < len(p.lines) && p.blank(r+d) {
		r += d
	}
	for r+d >= 0 && r+d < len(p.lines) && !p.blank(r+d) {
		r += d
	}
	if r+d >= 0 && r+d < len(p.lines) {
		r += d
	}
	p.row, p.col, p.want = r, 0, 0
}

// Search jumps to the next case-insensitive match of q and remembers it for n and N.
func (p *pager) Search(q string) bool {
	p.search = strings.ToLower(q)
	return p.find(1, 1)
}

func (p *pager) find(d, n int) bool {
	if p.search == "" {
		return false
	}
	q := []rune(p.search)
	r, c := p.row, p.col
	for range n {
		found := false
		for steps := 0; steps <= len(p.lines); steps++ {
			line := []rune(strings.ToLower(string(p.plain[r])))
			if d > 0 {
				from := c + 1
				if steps > 0 {
					from = 0
				}
				if i := indexRunes(line, q, from); i >= 0 {
					c, found = i, true
					break
				}
				r, c = (r+1)%len(p.lines), -1
			} else {
				to := c - 1
				if steps > 0 {
					to = len(line)
				}
				if i := lastIndexRunes(line, q, to); i >= 0 {
					c, found = i, true
					break
				}
				r = (r - 1 + len(p.lines)) % len(p.lines)
				c = len(p.plain[r]) + 1
			}
		}
		if !found {
			return false
		}
	}
	p.row, p.col, p.want = r, c, c
	p.scroll()
	return true
}

func indexRunes(s, q []rune, from int) int {
	for i := max(from, 0); i+len(q) <= len(s); i++ {
		if string(s[i:i+len(q)]) == string(q) {
			return i
		}
	}
	return -1
}

func lastIndexRunes(s, q []rune, to int) int {
	for i := min(to, len(s)-len(q)); i >= 0; i-- {
		if string(s[i:i+len(q)]) == string(q) {
			return i
		}
	}
	return -1
}
