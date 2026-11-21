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
// Glyphs are inset by consolePad so the text does not sit on the clip edge.
type consoleDoc struct {
	widget.BaseWidget

	lv     *logView
	scroll *container.Scroll

	metrics    []lineMetric
	metricSize float32
	metricGen  uint64
	maxW       float32

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
	c.prepareMetrics()
	n := len(c.metrics)
	lineH := consoleLineHeight()
	pad := consolePad()
	h := lineH + pad*2
	if n > 0 {
		h = lineH*float32(n) + pad*2
	}
	w := c.maxW + pad*2
	if w < 1 {
		w = 1
	}
	return fyne.NewSize(w, h)
}

// syncLines measures lines that were appended after the metrics already recorded.
// lines is the buffer the metrics describe, including any prefix just dropped.
func (c *consoleDoc) syncLines(lines []string) {
	c.metricSize = theme.TextSize()
	if len(lines) < len(c.metrics) {
		c.metrics = nil
		c.maxW = 0
		c.metricGen++
	}
	if len(lines) == len(c.metrics) {
		return
	}
	for _, line := range lines[len(c.metrics):] {
		m := measureLine(line)
		c.metrics = append(c.metrics, m)
		if m.width > c.maxW {
			c.maxW = m.width
		}
	}
}

func (c *consoleDoc) invalidate() {
	c.metrics = nil
	c.maxW = 0
	c.metricGen++
	c.sel = consoleSel{}
}

// dropPrefix forgets metrics and selection for lines trimmed off the front.
func (c *consoleDoc) dropPrefix(n int) {
	if n <= 0 {
		return
	}
	c.metricGen++
	if n >= len(c.metrics) {
		c.metrics = nil
		c.maxW = 0
	} else {
		c.metrics = c.metrics[n:]
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
	for _, m := range c.metrics {
		if m.width > c.maxW {
			c.maxW = m.width
		}
	}
}

// prepareMetrics rebuilds cached widths when the theme text size changes.
// Hit testing and selection painting read the cache instead of measuring on each move.
func (c *consoleDoc) prepareMetrics() {
	sz := theme.TextSize()
	if sz == c.metricSize {
		return
	}
	c.metricSize = sz
	c.metrics = nil
	c.maxW = 0
	c.metricGen++
	if c.lv != nil {
		c.syncLines(c.lv.copyLines())
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
	row, col := c.posToRowCol(e.Position)
	moved := !c.dragging || row != c.sel.bRow || col != c.sel.bCol
	c.dragging = true
	if moved {
		c.sel.bRow, c.sel.bCol = row, col
		c.sel.active = c.sel.aRow != row || c.sel.aCol != col
		c.Refresh()
	}
	c.reveal(e.Position)
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
	c.prepareMetrics()
	lineH := consoleLineHeight()
	if lineH < 1 {
		lineH = 1
	}
	pad := consolePad()
	y := p.Y - pad
	if y < 0 {
		y = 0
	}
	row := int(math.Floor(float64(y / lineH)))
	if row < 0 {
		row = 0
	}
	if row >= n {
		row = n - 1
	}
	c.ensureCuts(row)
	if row < 0 || row >= len(c.metrics) {
		return row, 0
	}
	return row, c.metrics[row].colAt(p.X - pad)
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
	pad := consolePad()
	first = int(math.Floor(float64((off - pad) / lineH)))
	last = int(math.Floor(float64((off+view-pad)/lineH))) + 1
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

	laid               bool
	first, last, rowsN int
	gen                uint64
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
	c.prepareMetrics()
	first, last := c.visible()
	if r.laid && r.first == first && r.last == last && r.gen == c.metricGen && r.rowsN == len(c.metrics) {
		if r.placeSelection(first, last) {
			r.syncObjects()
			r.root.Refresh()
		}
		return
	}
	r.layoutRows(first, last)
	r.placeSelection(first, last)
	r.syncObjects()
	r.root.Refresh()
	r.laid = true
	r.first, r.last = first, last
	r.gen = c.metricGen
	r.rowsN = len(c.metrics)
}

func (r *consoleRenderer) layoutRows(first, last int) {
	c := r.doc
	lineH := consoleLineHeight()
	n := 0
	if last >= first {
		n = last - first + 1
	}
	for len(r.rows) < n {
		r.rows = append(r.rows, newColorLine())
	}
	if n == 0 {
		for _, row := range r.rows {
			row.Hide()
		}
		return
	}
	lines := c.lv.slice(first, last)
	pad := consolePad()
	for i := 0; i < n; i++ {
		row := first + i
		raw := ""
		if i < len(lines) {
			raw = lines[i]
		}
		line := r.rows[i]
		line.Set(raw)
		w := float32(1)
		if row >= 0 && row < len(c.metrics) && c.metrics[row].width > 1 {
			w = c.metrics[row].width
		}
		line.Move(fyne.NewPos(pad, pad+float32(row)*lineH))
		line.Resize(fyne.NewSize(w, lineH))
		line.Show()
	}
	for i := n; i < len(r.rows); i++ {
		r.rows[i].Hide()
	}
}

// placeSelection moves the highlight rectangles. It reports whether the mark pool grew.
func (r *consoleRenderer) placeSelection(first, last int) bool {
	c := r.doc
	lineH := consoleLineHeight()
	pad := consolePad()
	selColor := theme.Color(theme.ColorNameSelection)
	markN := 0
	grew := false
	if c.sel.active && last >= first {
		for row := first; row <= last; row++ {
			if row < 0 || row >= len(c.metrics) {
				continue
			}
			m := c.metrics[row]
			start, end, ok := c.sel.rowSpan(row, m.runes())
			if !ok {
				continue
			}
			if start != 0 || end != m.runes() {
				c.ensureCuts(row)
				m = c.metrics[row]
			}
			if markN >= len(r.marks) {
				r.marks = append(r.marks, canvas.NewRectangle(selColor))
				grew = true
			}
			rect := r.marks[markN]
			markN++
			x1 := m.xAt(start)
			x2 := m.xAt(end)
			if x2 < x1 {
				x2 = x1
			}
			rect.FillColor = selColor
			rect.Move(fyne.NewPos(pad+x1, pad+float32(row)*lineH))
			rect.Resize(fyne.NewSize(fyne.Max(x2-x1, 1), lineH))
			rect.Show()
		}
	}
	for i := markN; i < len(r.marks); i++ {
		r.marks[i].Hide()
	}
	return grew
}

func (r *consoleRenderer) syncObjects() {
	objs := make([]fyne.CanvasObject, 0, len(r.marks)+len(r.rows))
	for _, m := range r.marks {
		objs = append(objs, m)
	}
	for _, row := range r.rows {
		objs = append(objs, row)
	}
	r.root.Objects = objs
}

var (
	cachedLineKey float32
	cachedLineH   float32
)

// consolePad is the gap between console glyphs and the edge of the document.
// The grey panel adds the same amount again outside the scroller.
func consolePad() float32 {
	return theme.InnerPadding()
}

func consoleLineHeight() float32 {
	sz := theme.TextSize()
	key := sz + theme.InnerPadding()
	if cachedLineH > 0 && cachedLineKey == key {
		return cachedLineH
	}
	h := colorLineHeight()
	if fyne.CurrentApp() != nil {
		if m := fyne.MeasureText("Ag", sz, consoleStyle).Height; m > h {
			h = m
		}
	}
	cachedLineKey = key
	cachedLineH = h
	return h
}

func monoWidth(s string) float32 {
	if s == "" {
		return 0
	}
	return fyne.MeasureText(s, theme.TextSize(), consoleStyle).Width
}

// lineMetric is the laid-out width of one console row. Spans match the colour
// runs drawn by colorLine, so the selection rectangle ends on the same edges
// as the glyphs. cuts inside a span are filled on first use.
type lineMetric struct {
	width float32
	spans []spanMetric
}

type spanMetric struct {
	n     int
	width float32
	cum   []float32 // len n+1 once measured; nil until then; empty means give up
}

func (m lineMetric) runes() int {
	n := 0
	for _, sp := range m.spans {
		n += sp.n
	}
	return n
}

func (m lineMetric) xAt(col int) float32 {
	if col <= 0 || len(m.spans) == 0 {
		return 0
	}
	x := float32(0)
	for _, sp := range m.spans {
		if col >= sp.n {
			x += sp.width
			col -= sp.n
			if col == 0 {
				return x
			}
			continue
		}
		return x + sp.xAt(col)
	}
	return x
}

func (m lineMetric) colAt(x float32) int {
	if x <= 0 || len(m.spans) == 0 {
		return 0
	}
	col := 0
	pos := float32(0)
	for i, sp := range m.spans {
		end := pos + sp.width
		if x < end || i == len(m.spans)-1 {
			return col + sp.colAt(x-pos)
		}
		pos = end
		col += sp.n
	}
	return col
}

func (sp spanMetric) xAt(col int) float32 {
	if col <= 0 || sp.n <= 0 {
		return 0
	}
	if col >= sp.n {
		return sp.width
	}
	if len(sp.cum) == sp.n+1 {
		return sp.cum[col]
	}
	return sp.width * float32(col) / float32(sp.n)
}

func (sp spanMetric) colAt(local float32) int {
	if sp.n <= 0 || local <= 0 {
		return 0
	}
	if len(sp.cum) == sp.n+1 {
		if local >= sp.cum[sp.n] {
			return sp.n
		}
		lo, hi := 0, sp.n
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if sp.cum[mid] <= local {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		if lo < sp.n && local-sp.cum[lo] > sp.cum[lo+1]-local {
			return lo + 1
		}
		return lo
	}
	if sp.width <= 0 {
		return 0
	}
	if local >= sp.width {
		return sp.n
	}
	step := sp.width / float32(sp.n)
	if step <= 0 {
		return 0
	}
	k := int(math.Floor(float64(local / step)))
	if k < 0 {
		k = 0
	}
	if k >= sp.n {
		return sp.n
	}
	if local-float32(k)*step > step*0.5 {
		k++
	}
	if k > sp.n {
		return sp.n
	}
	return k
}

func measureLine(raw string) lineMetric {
	texts := spanTexts(raw)
	if len(texts) == 0 {
		return lineMetric{}
	}
	spans := make([]spanMetric, 0, len(texts))
	var total float32
	runes := 0
	for _, text := range texts {
		sp := measureSpan(text)
		spans = append(spans, sp)
		total += sp.width
		runes += sp.n
	}
	if runes != utf8.RuneCountInString(stripANSI(raw)) {
		sp := measureSpan(stripANSI(raw))
		return lineMetric{width: sp.width, spans: []spanMetric{sp}}
	}
	return lineMetric{width: total, spans: spans}
}

func measureSpan(text string) spanMetric {
	return spanMetric{n: utf8.RuneCountInString(text), width: monoWidth(text)}
}

func spanTexts(raw string) []string {
	parts := parseANSI(raw)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p.Text != "" {
			out = append(out, p.Text)
		}
	}
	if len(out) == 0 {
		if plain := stripANSI(raw); plain != "" {
			return []string{plain}
		}
	}
	return out
}

// ensureCuts measures per-rune advances for one row the first time it is hit.
// Dragging then reuses the cuts instead of shaping text on every mouse move.
func (c *consoleDoc) ensureCuts(row int) {
	if row < 0 || row >= len(c.metrics) {
		return
	}
	m := &c.metrics[row]
	need := false
	for i := range m.spans {
		sp := &m.spans[i]
		if sp.n > 1 && sp.cum == nil {
			need = true
			break
		}
	}
	if !need {
		return
	}
	raw, ok := c.lv.lineAt(row)
	if !ok {
		return
	}
	texts := spanTexts(raw)
	if len(texts) != len(m.spans) {
		if len(m.spans) == 1 {
			fillCuts(&m.spans[0], stripANSI(raw))
			return
		}
		for i := range m.spans {
			if m.spans[i].cum == nil {
				m.spans[i].cum = []float32{}
			}
		}
		return
	}
	for i := range m.spans {
		if m.spans[i].n > 1 && m.spans[i].cum == nil {
			fillCuts(&m.spans[i], texts[i])
		}
	}
}

func fillCuts(sp *spanMetric, text string) {
	if sp.n <= 1 {
		return
	}
	rs := []rune(text)
	cum := make([]float32, sp.n+1)
	prev := float32(0)
	last := sp.n
	if last > len(rs) {
		last = len(rs)
	}
	for i := 1; i < last; i++ {
		cw := monoWidth(string(rs[:i]))
		if cw < prev {
			cw = prev
		}
		if sp.width > 0 && cw > sp.width {
			cw = sp.width
		}
		cum[i] = cw
		prev = cw
	}
	if last < sp.n && prev < sp.width {
		// Keep the tail monotonic if the text is shorter than the metric.
		for i := last; i < sp.n; i++ {
			cum[i] = prev
		}
	}
	cum[sp.n] = sp.width
	sp.cum = cum
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
