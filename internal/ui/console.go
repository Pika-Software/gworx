package ui

import (
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

var consoleStyle = fyne.TextStyle{Monospace: true}

// consoleDoc is the dedicated-server console: colored lines that can be
// drag-selected and copied. It virtualizes rows so a long log stays cheap.
type consoleDoc struct {
	widget.BaseWidget

	lv     *logView
	scroll *container.Scroll

	widths []float32
	maxW   float32

	sel        consoleSel
	stick      bool
	adjusting  bool
	dragging   bool
	lastDouble int64
}

type consoleSel struct {
	active     bool
	aRow, aCol int
	bRow, bCol int
}

func newConsoleDoc(lv *logView) *consoleDoc {
	c := &consoleDoc{lv: lv, stick: true}
	c.ExtendBaseWidget(c)
	return c
}

func (c *consoleDoc) CreateRenderer() fyne.WidgetRenderer {
	return &consoleRenderer{doc: c, root: container.NewWithoutLayout()}
}

func (c *consoleDoc) MinSize() fyne.Size {
	c.ExtendBaseWidget(c)
	return c.contentSize()
}

func (c *consoleDoc) contentSize() fyne.Size {
	n := len(c.widths)
	lineH := consoleLineHeight()
	h := lineH
	if n > 0 {
		h = lineH * float32(n)
	}
	w := c.maxW
	if w < 1 {
		w = 1
	}
	return fyne.NewSize(w, h)
}

// syncLines measures lines that were appended after the widths already recorded.
// lines is the buffer the widths describe, including any prefix just dropped.
func (c *consoleDoc) syncLines(lines []string) {
	if len(lines) < len(c.widths) {
		c.widths = nil
		c.maxW = 0
	}
	if len(lines) == len(c.widths) {
		return
	}
	for _, line := range lines[len(c.widths):] {
		w := monoWidth(stripANSI(line))
		c.widths = append(c.widths, w)
		if w > c.maxW {
			c.maxW = w
		}
	}
}

func (c *consoleDoc) invalidate() {
	c.widths = nil
	c.maxW = 0
	c.sel = consoleSel{}
}

// dropPrefix forgets widths and selection for lines trimmed off the front.
func (c *consoleDoc) dropPrefix(n int) {
	if n <= 0 {
		return
	}
	if n >= len(c.widths) {
		c.widths = nil
		c.maxW = 0
	} else {
		c.widths = c.widths[n:]
		c.recomputeMax()
	}
	if !c.sel.active {
		return
	}
	c.sel.aRow -= n
	c.sel.bRow -= n
	if c.sel.aRow < 0 && c.sel.bRow < 0 {
		c.sel = consoleSel{}
		return
	}
	if c.sel.aRow < 0 {
		c.sel.aRow, c.sel.aCol = 0, 0
	}
	if c.sel.bRow < 0 {
		c.sel.bRow, c.sel.bCol = 0, 0
	}
	if c.sel.aRow == c.sel.bRow && c.sel.aCol == c.sel.bCol {
		c.sel.active = false
	}
}

func (c *consoleDoc) recomputeMax() {
	c.maxW = 0
	for _, w := range c.widths {
		if w > c.maxW {
			c.maxW = w
		}
	}
}

func (c *consoleDoc) onScroll() {
	if !c.adjusting {
		c.stick = c.atBottom()
	}
	c.Refresh()
}

func (c *consoleDoc) atBottom() bool {
	if c.scroll == nil {
		return true
	}
	view := c.scroll.Size().Height
	content := c.MinSize().Height
	if content <= view+1 {
		return true
	}
	return c.scroll.Offset.Y >= content-view-consoleLineHeight()
}

func (c *consoleDoc) MouseDown(e *desktop.MouseEvent) {
	if e.Button != desktop.MouseButtonPrimary || c.lv.length() == 0 {
		return
	}
	row, col := c.posToRowCol(e.Position)
	if c.triple() {
		c.lastDouble = 0
		c.selectLine(row)
		c.focus()
		c.Refresh()
		return
	}
	if e.Modifier&fyne.KeyModifierShift != 0 && c.sel.active {
		c.sel.bRow, c.sel.bCol = row, col
		c.sel.active = c.sel.aRow != row || c.sel.aCol != col
	} else {
		c.sel.aRow, c.sel.aCol = row, col
		c.sel.bRow, c.sel.bCol = row, col
		c.sel.active = false
	}
	c.focus()
	c.Refresh()
}

func (c *consoleDoc) MouseUp(*desktop.MouseEvent) {}

func (c *consoleDoc) Dragged(e *fyne.DragEvent) {
	if c.lv.length() == 0 {
		return
	}
	c.dragging = true
	row, col := c.posToRowCol(e.Position)
	c.sel.bRow, c.sel.bCol = row, col
	c.sel.active = c.sel.aRow != row || c.sel.aCol != col
	c.reveal(e.Position)
	c.Refresh()
}

func (c *consoleDoc) DragEnd() { c.dragging = false }

func (c *consoleDoc) DoubleTapped(e *fyne.PointEvent) {
	if c.lv.length() == 0 {
		return
	}
	c.lastDouble = time.Now().UnixMilli()
	row, col := c.posToRowCol(e.Position)
	a, b := tokenBounds(c.plain(row), col)
	c.sel.aRow, c.sel.bRow = row, row
	c.sel.aCol, c.sel.bCol = a, b
	c.sel.active = a != b
	c.focus()
	c.Refresh()
}

func (c *consoleDoc) TappedSecondary(ev *fyne.PointEvent) {
	app := fyne.CurrentApp()
	if app == nil {
		return
	}
	can := app.Driver().CanvasForObject(c)
	if can == nil {
		return
	}
	row, _ := c.posToRowCol(ev.Position)
	var items []*fyne.MenuItem
	if c.sel.active {
		if text := c.selectedText(); text != "" {
			items = append(items, fyne.NewMenuItem("Copy", func() { copyText(text, nil) }))
		}
	}
	if line := c.plain(row); line != "" {
		items = append(items, fyne.NewMenuItem("Copy line", func() { copyText(line, nil) }))
	}
	if c.lv.length() > 0 {
		items = append(items, fyne.NewMenuItem("Select all", func() { c.selectAll() }))
	}
	if len(items) == 0 {
		return
	}
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", items...), can, ev.AbsolutePosition)
}

func (c *consoleDoc) TypedShortcut(s fyne.Shortcut) {
	switch sc := s.(type) {
	case *fyne.ShortcutCopy:
		copyText(c.selectedText(), sc.Clipboard)
	case *fyne.ShortcutSelectAll:
		c.selectAll()
	}
}

func (c *consoleDoc) TypedKey(*fyne.KeyEvent) {}
func (c *consoleDoc) TypedRune(rune)          {}
func (c *consoleDoc) FocusGained()            {}
func (c *consoleDoc) FocusLost()              {}

func (c *consoleDoc) Cursor() desktop.Cursor { return desktop.TextCursor }

func (c *consoleDoc) selectAll() {
	n := c.lv.length()
	if n == 0 {
		return
	}
	c.sel.aRow, c.sel.aCol = 0, 0
	c.sel.bRow = n - 1
	c.sel.bCol = utf8.RuneCountInString(c.plain(n - 1))
	c.sel.active = true
	c.Refresh()
}

func (c *consoleDoc) selectLine(row int) {
	if row < 0 || row >= c.lv.length() {
		return
	}
	c.sel.aRow, c.sel.aCol = row, 0
	c.sel.bRow = row
	c.sel.bCol = utf8.RuneCountInString(c.plain(row))
	c.sel.active = true
}

func (c *consoleDoc) selectedText() string {
	if !c.sel.active {
		return ""
	}
	r1, c1, r2, c2 := c.sel.ordered()
	lines := c.lv.plainRange(r1, r2)
	if len(lines) == 0 {
		return ""
	}
	return selectionText(lines, 0, c1, len(lines)-1, c2)
}

func (c *consoleDoc) focus() {
	app := fyne.CurrentApp()
	if app == nil {
		return
	}
	if can := app.Driver().CanvasForObject(c); can != nil {
		can.Focus(c)
	}
}

func (c *consoleDoc) triple() bool {
	if c.lastDouble == 0 {
		return false
	}
	app := fyne.CurrentApp()
	if app == nil || app.Driver() == nil {
		return false
	}
	return time.Now().UnixMilli()-c.lastDouble <= app.Driver().DoubleTapDelay().Milliseconds()
}

func (c *consoleDoc) plain(row int) string {
	s, ok := c.lv.lineAt(row)
	if !ok {
		return ""
	}
	return stripANSI(s)
}

func (c *consoleDoc) posToRowCol(p fyne.Position) (int, int) {
	n := c.lv.length()
	if n == 0 {
		return 0, 0
	}
	lineH := consoleLineHeight()
	if lineH < 1 {
		lineH = 1
	}
	row := int(math.Floor(float64(p.Y / lineH)))
	if row < 0 {
		row = 0
	}
	if row >= n {
		row = n - 1
	}
	return row, colAt(c.plain(row), p.X)
}

func (c *consoleDoc) reveal(p fyne.Position) {
	if c.scroll == nil {
		return
	}
	off := c.scroll.Offset
	size := c.scroll.Size()
	lineH := consoleLineHeight()
	nx, ny := off.X, off.Y
	if p.X < off.X {
		nx = p.X
	} else if size.Width > 0 && p.X > off.X+size.Width-4 {
		nx = p.X - size.Width + 4
	}
	if p.Y < off.Y {
		ny = p.Y
	} else if size.Height > 0 && p.Y+lineH > off.Y+size.Height {
		ny = p.Y + lineH - size.Height
	}
	if nx < 0 {
		nx = 0
	}
	if ny < 0 {
		ny = 0
	}
	if nx != off.X || ny != off.Y {
		c.scroll.ScrollToOffset(fyne.NewPos(nx, ny))
	}
}

func (c *consoleDoc) visible() (first, last int) {
	n := c.lv.length()
	if n == 0 {
		return 0, -1
	}
	lineH := consoleLineHeight()
	if lineH < 1 {
		lineH = 1
	}
	off := float32(0)
	view := c.Size().Height
	if c.scroll != nil && c.scroll.Size().Height > 0 {
		off = c.scroll.Offset.Y
		view = c.scroll.Size().Height
	}
	if view < lineH {
		view = lineH
	}
	first = int(math.Floor(float64(off / lineH)))
	last = int(math.Floor(float64((off+view)/lineH))) + 1
	if first < 0 {
		first = 0
	}
	if last >= n {
		last = n - 1
	}
	if first > last {
		return 0, -1
	}
	return first, last
}

func (s consoleSel) ordered() (r1, c1, r2, c2 int) {
	r1, c1, r2, c2 = s.aRow, s.aCol, s.bRow, s.bCol
	if r1 > r2 || (r1 == r2 && c1 > c2) {
		r1, c1, r2, c2 = r2, c2, r1, c1
	}
	return
}

// rowSpan is the half-open column range selected on row.
func (s consoleSel) rowSpan(row, lineLen int) (start, end int, ok bool) {
	if !s.active {
		return 0, 0, false
	}
	r1, c1, r2, c2 := s.ordered()
	if row < r1 || row > r2 {
		return 0, 0, false
	}
	start, end = 0, lineLen
	if row == r1 {
		start = c1
	}
	if row == r2 {
		end = c2
	}
	start = clampInt(start, 0, lineLen)
	end = clampInt(end, 0, lineLen)
	if start > end {
		start = end
	}
	return start, end, start != end
}

type consoleRenderer struct {
	doc   *consoleDoc
	root  *fyne.Container
	rows  []*colorLine
	marks []*canvas.Rectangle
}

func (r *consoleRenderer) Destroy() {}

func (r *consoleRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.root}
}

func (r *consoleRenderer) Layout(size fyne.Size) {
	r.root.Resize(size)
	r.arrange()
}

func (r *consoleRenderer) MinSize() fyne.Size { return r.doc.contentSize() }

func (r *consoleRenderer) Refresh() {
	r.arrange()
	canvas.Refresh(r.doc)
}

func (r *consoleRenderer) arrange() {
	c := r.doc
	first, last := c.visible()
	lineH := consoleLineHeight()
	var lines []string
	if last >= first {
		lines = c.lv.slice(first, last)
	}
	for len(r.rows) < len(lines) {
		r.rows = append(r.rows, newColorLine())
	}

	objs := make([]fyne.CanvasObject, 0, len(lines)*2)
	markN := 0
	selColor := theme.Color(theme.ColorNameSelection)
	for i, raw := range lines {
		row := first + i
		plain := stripANSI(raw)
		runes := []rune(plain)
		if start, end, ok := c.sel.rowSpan(row, len(runes)); ok {
			if markN >= len(r.marks) {
				r.marks = append(r.marks, canvas.NewRectangle(selColor))
			}
			rect := r.marks[markN]
			markN++
			rect.FillColor = selColor
			x1 := monoWidth(string(runes[:start]))
			x2 := monoWidth(string(runes[:end]))
			if x2 < x1 {
				x2 = x1
			}
			rect.Move(fyne.NewPos(x1, float32(row)*lineH))
			rect.Resize(fyne.NewSize(fyne.Max(x2-x1, 1), lineH))
			rect.Show()
			objs = append(objs, rect)
		}
	}
	for i, raw := range lines {
		line := r.rows[i]
		line.Set(raw)
		w := monoWidth(stripANSI(raw))
		if w < 1 {
			w = 1
		}
		line.Move(fyne.NewPos(0, float32(first+i)*lineH))
		line.Resize(fyne.NewSize(w, lineH))
		line.Show()
		objs = append(objs, line)
	}
	r.root.Objects = objs
	r.root.Refresh()
}

func consoleLineHeight() float32 {
	h := colorLineHeight()
	if fyne.CurrentApp() == nil {
		return h
	}
	if m := fyne.MeasureText("Ag", theme.TextSize(), consoleStyle).Height; m > h {
		h = m
	}
	return h
}

func monoWidth(s string) float32 {
	if s == "" {
		return 0
	}
	return fyne.MeasureText(s, theme.TextSize(), consoleStyle).Width
}

func colAt(line string, x float32) int {
	r := []rune(line)
	if len(r) == 0 || x <= 0 {
		return 0
	}
	lo, hi := 0, len(r)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if monoWidth(string(r[:mid])) <= x {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo < len(r) {
		left := monoWidth(string(r[:lo]))
		right := monoWidth(string(r[:lo+1]))
		if x-left > right-x {
			return lo + 1
		}
	}
	return lo
}

func copyText(s string, clip fyne.Clipboard) {
	if s == "" {
		return
	}
	if clip == nil {
		if app := fyne.CurrentApp(); app != nil {
			clip = app.Clipboard()
		}
	}
	if clip != nil {
		clip.SetContent(s)
	}
}

// selectionText returns the plain text between two caret positions.
// Columns are rune indexes. Either point may come first.
func selectionText(lines []string, r1, c1, r2, c2 int) string {
	if len(lines) == 0 {
		return ""
	}
	if r1 > r2 || (r1 == r2 && c1 > c2) {
		r1, c1, r2, c2 = r2, c2, r1, c1
	}
	if r1 < 0 {
		r1 = 0
	}
	if r2 >= len(lines) {
		r2 = len(lines) - 1
	}
	if r1 > r2 {
		return ""
	}
	var b strings.Builder
	for r := r1; r <= r2; r++ {
		runes := []rune(lines[r])
		a, z := 0, len(runes)
		if r == r1 {
			a = clampInt(c1, 0, len(runes))
		}
		if r == r2 {
			z = clampInt(c2, 0, len(runes))
		}
		if z < a {
			z = a
		}
		if r > r1 {
			b.WriteByte('\n')
		}
		b.WriteString(string(runes[a:z]))
	}
	return b.String()
}

func tokenBounds(line string, col int) (start, end int) {
	r := []rune(line)
	n := len(r)
	if n == 0 {
		return 0, 0
	}
	if col < 0 {
		col = 0
	}
	if col >= n {
		col = n - 1
	}
	word := func(ch rune) bool { return !unicode.IsSpace(ch) }
	if !word(r[col]) {
		s, e := col, col+1
		for s > 0 && !word(r[s-1]) {
			s--
		}
		for e < n && !word(r[e]) {
			e++
		}
		return s, e
	}
	s, e := col, col+1
	for s > 0 && word(r[s-1]) {
		s--
	}
	for e < n && word(r[e]) {
		e++
	}
	return s, e
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

var (
	_ fyne.Widget            = (*consoleDoc)(nil)
	_ fyne.Draggable         = (*consoleDoc)(nil)
	_ fyne.DoubleTappable    = (*consoleDoc)(nil)
	_ fyne.SecondaryTappable = (*consoleDoc)(nil)
	_ fyne.Focusable         = (*consoleDoc)(nil)
	_ fyne.Shortcutable      = (*consoleDoc)(nil)
	_ desktop.Mouseable      = (*consoleDoc)(nil)
	_ desktop.Cursorable     = (*consoleDoc)(nil)
)
