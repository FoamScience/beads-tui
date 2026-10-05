package ui

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/charmbracelet/x/ansi"
)

const (
	boxW   = 28
	boxH   = 4
	gapX   = 6
	pitchY = boxH + 1
)

// gnode is a box in the layered layout; dummies carry long edges through intermediate layers.
type gnode struct {
	id       string
	is       *bd.Issue
	layer    int
	pos      int
	external bool
	dummy    bool
	edge     [2]string // dummy: the original prerequisite -> dependent edge
}

type gedge struct {
	from, to *gnode
	orig     [2]string // prerequisite id, dependent id
}

type graphView struct {
	fixed      *bd.Snapshot // set for formula previews; nil means the live DB
	rootID     string
	showClosed bool
	sel        string
	nodes      map[string]*gnode
	layers     [][]*gnode
	edges      []gedge
	offX, offY int
}

func newGraph() *graphView { return &graphView{} }

func (v *graphView) Name() string { return "Graph" }

func (v *graphView) src(a *App) *bd.Snapshot {
	if v.fixed != nil {
		return v.fixed
	}
	return a.snap
}

func (v *graphView) Selected() *bd.Issue {
	if n := v.nodes[v.sel]; n != nil {
		return n.is
	}
	return nil
}

func (v *graphView) setRoot(a *App, is *bd.Issue) {
	root := is
	if is.IssueType != "epic" {
		if e := v.src(a).Epic(is); e != nil {
			root = e
		}
	}
	v.rootID, v.offX, v.offY = root.ID, 0, 0
	v.sel = is.ID
	v.Rebuild(a)
}

// members returns the issues drawn for the root: an epic's non-epic descendants,
// or the blocking closure of a lone issue.
func (v *graphView) members(a *App) map[string]bool {
	s := v.src(a)
	set := map[string]bool{}
	root := s.ByID[v.rootID]
	if root == nil {
		return set
	}
	keep := func(is *bd.Issue) bool { return v.showClosed || !is.Closed() }
	if root.IssueType == "epic" || len(s.Children[root.ID]) > 0 {
		var walk func(id string)
		walk = func(id string) {
			for _, c := range s.Children[id] {
				if c.IssueType != "epic" && keep(c) {
					set[c.ID] = true
				}
				walk(c.ID)
			}
		}
		walk(root.ID)
		return set
	}
	queue := []string{root.ID}
	set[root.ID] = true
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		next := []string{}
		for _, d := range s.ByID[id].Dependencies {
			if bd.Blocking(d.Type) {
				next = append(next, d.DependsOnID)
			}
		}
		for _, d := range s.Dependents(id) {
			if bd.Blocking(d.Type) {
				next = append(next, d.IssueID)
			}
		}
		for _, n := range next {
			if t := s.ByID[n]; t != nil && !set[n] && keep(t) {
				set[n] = true
				queue = append(queue, n)
			}
		}
	}
	return set
}

func (v *graphView) Rebuild(a *App) {
	v.nodes, v.layers, v.edges = map[string]*gnode{}, nil, nil
	if v.rootID == "" {
		return
	}
	s := v.src(a)
	set := v.members(a)
	preds := map[string][]string{}
	for id := range set {
		for _, d := range s.ByID[id].Dependencies {
			if !bd.Blocking(d.Type) {
				continue
			}
			t := s.ByID[d.DependsOnID]
			if t == nil || (!v.showClosed && t.Closed()) {
				continue
			}
			preds[id] = append(preds[id], t.ID)
			if !set[t.ID] {
				v.nodes[t.ID] = &gnode{id: t.ID, is: t, external: true}
			}
		}
	}
	for id := range set {
		v.nodes[id] = &gnode{id: id, is: s.ByID[id]}
	}
	// longest-path layering; a cycle member is cut at the revisit
	state := map[string]int{}
	var layer func(id string) int
	layer = func(id string) int {
		switch state[id] {
		case 1:
			return -1
		case 2:
			return v.nodes[id].layer
		}
		state[id] = 1
		l := 0
		for _, p := range preds[id] {
			l = max(l, layer(p)+1)
		}
		state[id] = 2
		v.nodes[id].layer = l
		return l
	}
	ids := make([]string, 0, len(v.nodes))
	for id := range v.nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return bd.IDLess(ids[i], ids[j]) })
	maxL := 0
	for _, id := range ids {
		maxL = max(maxL, layer(id))
	}
	v.layers = make([][]*gnode, maxL+1)
	for _, id := range ids {
		n := v.nodes[id]
		v.layers[n.layer] = append(v.layers[n.layer], n)
	}
	for _, id := range ids {
		for _, p := range preds[id] {
			v.addEdge(v.nodes[p], v.nodes[id])
		}
	}
	v.order()
	if v.nodes[v.sel] == nil || v.nodes[v.sel].dummy {
		v.sel = ""
		if len(v.layers) > 0 && len(v.layers[0]) > 0 {
			v.sel = v.layers[0][0].id
		}
	}
}

// addEdge links prerequisite p to dependent d, inserting dummies for every skipped layer.
func (v *graphView) addEdge(p, d *gnode) {
	if p.layer >= d.layer {
		return
	}
	orig := [2]string{p.id, d.id}
	prev := p
	for l := p.layer + 1; l < d.layer; l++ {
		dm := &gnode{id: fmt.Sprintf("%s>%s@%d", p.id, d.id, l), layer: l, dummy: true, edge: orig}
		v.nodes[dm.id] = dm
		v.layers[l] = append(v.layers[l], dm)
		v.edges = append(v.edges, gedge{prev, dm, orig})
		prev = dm
	}
	v.edges = append(v.edges, gedge{prev, d, orig})
}

// order applies two barycenter sweeps so edges mostly run straight.
func (v *graphView) order() {
	in := map[*gnode][]*gnode{}
	out := map[*gnode][]*gnode{}
	for _, e := range v.edges {
		in[e.to] = append(in[e.to], e.from)
		out[e.from] = append(out[e.from], e.to)
	}
	renumber := func(l []*gnode) {
		for i, n := range l {
			n.pos = i
		}
	}
	for _, l := range v.layers {
		renumber(l)
	}
	sweep := func(l []*gnode, nb map[*gnode][]*gnode) {
		bc := map[*gnode]float64{}
		for _, n := range l {
			if len(nb[n]) == 0 {
				bc[n] = float64(n.pos)
				continue
			}
			sum := 0.0
			for _, m := range nb[n] {
				sum += float64(m.pos)
			}
			bc[n] = sum / float64(len(nb[n]))
		}
		sort.SliceStable(l, func(i, j int) bool { return bc[l[i]] < bc[l[j]] })
		renumber(l)
	}
	for range 2 {
		for i := 1; i < len(v.layers); i++ {
			sweep(v.layers[i], in)
		}
		for i := len(v.layers) - 2; i >= 0; i-- {
			sweep(v.layers[i], out)
		}
	}
}

// closure returns every node reachable from id along edges in the given direction.
func (v *graphView) closure(id string, up bool) map[string]bool {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(x string) {
		for _, e := range v.edges {
			from, to := e.orig[0], e.orig[1]
			if up && to == x && !seen[from] {
				seen[from] = true
				walk(from)
			} else if !up && from == x && !seen[to] {
				seen[to] = true
				walk(to)
			}
		}
	}
	walk(id)
	return seen
}

func (v *graphView) Update(a *App, k tea.KeyPressMsg) (bool, tea.Cmd) {
	n := v.nodes[v.sel]
	switch k.String() {
	case "g":
		return true, nil
	case "H":
		v.showClosed = !v.showClosed
		v.Rebuild(a)
		return true, nil
	case "E":
		a.modal = v.epicPicker(a)
		return true, nil
	case "j", "down", "k", "up":
		if n != nil {
			d := 1
			if k.String() == "k" || k.String() == "up" {
				d = -1
			}
			l := v.layers[n.layer]
			for i := n.pos + d; i >= 0 && i < len(l); i += d {
				if !l[i].dummy {
					v.sel = l[i].id
					break
				}
			}
		}
		return true, nil
	case "h", "left", "l", "right":
		if n != nil {
			d := 1
			if k.String() == "h" || k.String() == "left" {
				d = -1
			}
			for li := n.layer + d; li >= 0 && li < len(v.layers); li += d {
				if best := nearest(v.layers[li], n.pos); best != nil {
					v.sel = best.id
					break
				}
			}
		}
		return true, nil
	case "D":
		if n == nil {
			return true, nil
		}
		a.modal = v.addDepModal(a, n.is)
		return true, nil
	case "X":
		if n == nil {
			return true, nil
		}
		a.modal = v.removeDepModal(a, n.is)
		return true, nil
	}
	return false, nil
}

func nearest(l []*gnode, pos int) *gnode {
	var best *gnode
	for _, m := range l {
		if m.dummy {
			continue
		}
		if best == nil || abs(m.pos-pos) < abs(best.pos-pos) {
			best = m
		}
	}
	return best
}

func abs(x int) int { return max(x, -x) }

func (v *graphView) epicPicker(a *App) *modal {
	var opts []option
	for _, is := range a.snap.Issues {
		if is.IssueType == "epic" && !is.Closed() {
			opts = append(opts, option{is.ID, a.shortID(is.ID) + "  " + is.Title})
		}
	}
	return newPicker("Graph of epic", opts, func(id string) tea.Cmd {
		if is := a.snap.ByID[id]; is != nil {
			v.setRoot(a, is)
		}
		return nil
	})
}

var depTypes = []string{"blocks", "related", "discovered-from", "tracks", "caused-by", "validates", "supersedes"}

func (v *graphView) addDepModal(a *App, is *bd.Issue) *modal {
	var opts []option
	for _, t := range a.snap.Issues {
		if t.ID != is.ID && !t.Closed() {
			mark := " "
			if v.nodes[t.ID] != nil {
				mark = "•"
			}
			opts = append(opts, option{t.ID, mark + " " + a.shortID(t.ID) + "  " + t.Title})
		}
	}
	slices.SortStableFunc(opts, func(x, y option) int { return strings.Compare(y.label[:1], x.label[:1]) })
	return newPicker(a.shortID(is.ID)+" needs…", opts, func(target string) tea.Cmd {
		var tops []option
		for _, t := range depTypes {
			tops = append(tops, option{t, t})
		}
		a.modal = newPicker("Edge type", tops, func(typ string) tea.Cmd {
			return v.depWrite(a, fmt.Sprintf("%s needs %s (%s)", a.shortID(is.ID), a.shortID(target), typ), "dep", "add", is.ID, target, "--type", typ)
		})
		return nil
	})
}

func (v *graphView) removeDepModal(a *App, is *bd.Issue) *modal {
	var opts []option
	for _, d := range is.Dependencies {
		if d.Type == "parent-child" {
			continue
		}
		title := ""
		if t := a.snap.ByID[d.DependsOnID]; t != nil {
			title = t.Title
		}
		opts = append(opts, option{d.DependsOnID, fmt.Sprintf("%-16s %s  %s", d.Type, a.shortID(d.DependsOnID), title)})
	}
	if len(opts) == 0 {
		return newConfirm(a.shortID(is.ID)+" has no edges to remove. Close?", func() tea.Cmd { return nil })
	}
	return newPicker("Remove edge from "+a.shortID(is.ID), opts, func(target string) tea.Cmd {
		return v.depWrite(a, "removed "+a.shortID(is.ID)+" → "+a.shortID(target), "dep", "remove", is.ID, target)
	})
}

// depWrite edits an edge, then lints the root epic so a new cycle shows up at once.
func (v *graphView) depWrite(a *App, desc string, args ...string) tea.Cmd {
	root := v.rootID
	r := v.src(a).ByID[root]
	lint := r != nil && r.IssueType == "epic"
	a.pending++
	c := a.client
	return func() tea.Msg {
		if err := c.Exec(args...); err != nil {
			return writeDoneMsg{desc, err}
		}
		if !lint {
			return writeDoneMsg{desc, nil}
		}
		out, err := c.Output("swarm", "validate", root)
		if err != nil {
			return writeDoneMsg{desc + " · lint failed", err}
		}
		if res := string(out); !strings.Contains(res, "Swarmable: YES") || strings.Contains(res, "⚠") {
			return writeDoneMsg{desc, fmt.Errorf("%s · lint: %s", desc, lintSummary(res))}
		}
		return writeDoneMsg{desc + " · lint ok", nil}
	}
}

func (v *graphView) Hints() []string {
	return []string{"hjkl move", "D add edge", "X remove edge", "E pick epic", "H closed"}
}

// ---- rendering ----

const (
	dirL = 1 << iota
	dirR
	dirU
	dirD
)

var lineRune = map[uint8]rune{
	dirL | dirR: '─', dirL: '─', dirR: '─',
	dirU | dirD: '│', dirU: '│', dirD: '│',
	dirR | dirD: '╭', dirL | dirD: '╮', dirR | dirU: '╰', dirL | dirU: '╯',
	dirL | dirR | dirD: '┬', dirL | dirR | dirU: '┴',
	dirU | dirD | dirR: '├', dirU | dirD | dirL: '┤',
	dirL | dirR | dirU | dirD: '┼',
}

type cell struct {
	r    rune
	mask uint8
	st   int
	skip bool // right half of a wide rune
}

const (
	stNone = iota
	stDim
	stHi
	stWarn
	stErr
	stOK
	stBold
	stBlue
)

var cellStyles = map[int]func(string) string{
	stDim:  func(s string) string { return sDim.Render(s) },
	stHi:   func(s string) string { return sAccent.Render(s) },
	stWarn: func(s string) string { return sWarn.Render(s) },
	stErr:  func(s string) string { return sErr.Render(s) },
	stOK:   func(s string) string { return sOK.Render(s) },
	stBold: func(s string) string { return sBold.Render(s) },
	stBlue: func(s string) string { return sDim.Foreground(cBlue).Render(s) },
}

type canvas struct {
	w, h  int
	cells [][]cell
}

func newCanvas(w, h int) *canvas {
	c := &canvas{w: w, h: h, cells: make([][]cell, h)}
	for i := range c.cells {
		c.cells[i] = make([]cell, w)
	}
	return c
}

func (c *canvas) line(x, y int, mask uint8, st int) {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return
	}
	cl := &c.cells[y][x]
	cl.mask |= mask
	if st == stHi || cl.st == stNone {
		cl.st = st
	}
}

func (c *canvas) hline(x1, x2, y, st int) {
	if x1 > x2 {
		x1, x2 = x2, x1
	}
	for x := x1; x <= x2; x++ {
		var m uint8
		if x > x1 {
			m |= dirL
		}
		if x < x2 {
			m |= dirR
		}
		c.line(x, y, m, st)
	}
}

func (c *canvas) vline(x, y1, y2, st int) {
	if y1 > y2 {
		y1, y2 = y2, y1
	}
	for y := y1; y <= y2; y++ {
		var m uint8
		if y > y1 {
			m |= dirU
		}
		if y < y2 {
			m |= dirD
		}
		c.line(x, y, m, st)
	}
}

func (c *canvas) text(x, y int, s string, st int) {
	for _, r := range s {
		if x >= c.w || y >= c.h || y < 0 {
			return
		}
		rw := max(ansi.StringWidth(string(r)), 1)
		if x >= 0 {
			c.cells[y][x] = cell{r: r, st: st}
			if rw == 2 && x+1 < c.w {
				c.cells[y][x+1] = cell{skip: true}
			}
		}
		x += rw
	}
}

func (c *canvas) render(x0, y0, w, h int) string {
	var b strings.Builder
	for y := y0; y < y0+h; y++ {
		if y > y0 {
			b.WriteByte('\n')
		}
		if y < 0 || y >= c.h {
			continue
		}
		run, st := []rune{}, -1
		flush := func() {
			if len(run) == 0 {
				return
			}
			if f := cellStyles[st]; f != nil {
				b.WriteString(f(string(run)))
			} else {
				b.WriteString(string(run))
			}
			run = run[:0]
		}
		for x := x0; x < min(x0+w, c.w); x++ {
			cl := c.cells[y][x]
			if cl.skip {
				continue
			}
			r := cl.r
			if r == 0 {
				r = ' '
				if cl.mask != 0 {
					r = lineRune[cl.mask]
				}
			}
			if cl.st != st {
				flush()
				st = cl.st
			}
			run = append(run, r)
		}
		flush()
	}
	return b.String()
}

func nodeXY(n *gnode) (int, int) { return 2 + n.layer*(boxW+gapX), n.pos * pitchY }

func statusStyle(s *bd.Snapshot, is *bd.Issue) int {
	switch {
	case is.Status == "in_progress":
		return stWarn
	case is.Closed():
		return stOK
	case is.Status == "deferred":
		return stBlue
	case s.IsBlocked(is):
		return stErr
	}
	return stNone
}

func (v *graphView) Render(a *App, w, h int) string {
	root := v.src(a).ByID[v.rootID]
	if root == nil {
		return sDim.Render("\n  Press g on any issue to graph its epic, or E to pick an epic.")
	}
	shown := 0
	for _, n := range v.nodes {
		if !n.dummy {
			shown++
		}
	}
	head := fmt.Sprintf(" %s %s  %s", sAccent.Render(a.shortID(root.ID)), sBold.Render(root.Title),
		sDim.Render(fmt.Sprintf("%d issues · %d layers", shown, len(v.layers))))
	if v.showClosed {
		head += sDim.Render(" · closed shown")
	}
	head = ansi.Truncate(head, w, "…")
	if shown == 0 {
		return head + "\n\n" + sDim.Render("  no open issues under this epic (H shows closed)")
	}
	maxPos := 0
	for _, l := range v.layers {
		maxPos = max(maxPos, len(l))
	}
	cv := newCanvas(2+len(v.layers)*(boxW+gapX), maxPos*pitchY)
	up, down := v.closure(v.sel, true), v.closure(v.sel, false)
	hot := func(e gedge) bool {
		p, d := e.orig[0], e.orig[1]
		return (up[p] && (up[d] || d == v.sel)) || (down[d] && (down[p] || p == v.sel))
	}
	// cold edges first so highlighted ones win shared cells
	for pass := range 2 {
		for _, e := range v.edges {
			if hot(e) != (pass == 1) {
				continue
			}
			st := stDim
			if pass == 1 {
				st = stHi
			}
			fx, fy := nodeXY(e.from)
			tx, ty := nodeXY(e.to)
			sx, sy := fx+boxW, fy+1
			xm := tx - 3
			cv.hline(sx, xm, sy, st)
			cv.vline(xm, sy, ty+1, st)
			if e.to.dummy {
				cv.hline(xm, tx+boxW, ty+1, st)
			} else {
				cv.hline(xm, tx-2, ty+1, st)
				cv.text(tx-1, ty+1, "▶", st)
			}
		}
	}
	for _, l := range v.layers {
		for _, n := range l {
			if !n.dummy {
				v.drawBox(a, cv, n, up[n.id] || down[n.id])
			}
		}
	}
	sel := v.nodes[v.sel]
	bodyH := h - 2
	if sel != nil {
		x, y := nodeXY(sel)
		if x < v.offX {
			v.offX = max(x-2, 0)
		}
		if x+boxW+2 > v.offX+w {
			v.offX = x + boxW + 2 - w
		}
		if y < v.offY {
			v.offY = y
		}
		if y+boxH > v.offY+bodyH {
			v.offY = y + boxH - bodyH
		}
	}
	v.offX = clamp(v.offX, 0, max(cv.w-w, 0))
	v.offY = clamp(v.offY, 0, max(cv.h-bodyH, 0))
	return head + "\n\n" + cv.render(v.offX, v.offY, w, bodyH)
}

func (v *graphView) drawBox(a *App, cv *canvas, n *gnode, related bool) {
	x, y := nodeXY(n)
	is := n.is
	border := stDim
	switch {
	case n.id == v.sel:
		border = stHi
	case related:
		border = stNone
	}
	tl, tr, bl, br, hz, vt := "╭", "╮", "╰", "╯", "─", "│"
	if n.external {
		tl, tr, bl, br, hz, vt = "┌", "┐", "└", "┘", "┄", "┆"
	}
	if n.id == v.sel {
		tl, tr, bl, br, hz, vt = "┏", "┓", "┗", "┛", "━", "┃"
	}
	cv.text(x, y, tl+strings.Repeat(hz, boxW-2)+tr, border)
	cv.text(x, y+boxH-1, bl+strings.Repeat(hz, boxW-2)+br, border)
	for r := 1; r < boxH-1; r++ {
		cv.text(x, y+r, vt+strings.Repeat(" ", boxW-2)+vt, border)
	}
	st := statusStyle(v.src(a), is)
	g := statusGlyph[is.Status]
	if is.Status == "open" && v.src(a).IsBlocked(is) {
		g = statusGlyph["blocked"]
	}
	cv.text(x+2, y+1, g, st)
	titleSt := stNone
	if n.id == v.sel {
		titleSt = stBold
	}
	if is.Closed() {
		titleSt = stDim
	}
	cv.text(x+4, y+1, ansi.Truncate(is.Title, boxW-6, "…"), titleSt)
	meta := fmt.Sprintf("%s P%d", a.shortID(is.ID), is.Priority)
	if is.EstimatedMinutes > 0 {
		meta += " " + minutes(is.EstimatedMinutes)
	}
	if n.external {
		meta += " ext"
	}
	cv.text(x+2, y+2, ansi.Truncate(meta, boxW-4, "…"), stDim)
}
