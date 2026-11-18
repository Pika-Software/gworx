package ui

import (
	"image/color"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const maxLogLines = 5000

// logView is a scrolling list of log lines, safe to append to from any goroutine.
// The server console (color, no timestamp) is a selectable document instead of
// a list, so a range of output can be highlighted and copied.
type logView struct {
	mu      sync.Mutex
	lines   []string
	pending bool
	dropped int // lines trimmed from the front since the last UI refresh
	stamp   bool
	color   bool // parse ANSI SGR colours (server console)
	list    *widget.List
	doc     *consoleDoc
	scroll  *container.Scroll
}

func newLogView() *logView {
	return newLogViewWith(true)
}

// newLogViewWith creates a log view; stamp prefixes lines with the time.
func newLogViewWith(stamp bool) *logView {
	lv := &logView{stamp: stamp, color: !stamp}
	if lv.color {
		lv.doc = newConsoleDoc(lv)
		lv.scroll = container.NewScroll(lv.doc)
		lv.doc.scroll = lv.scroll
		lv.scroll.OnScrolled = func(fyne.Position) { lv.doc.onScroll() }
		return lv
	}
	lv.list = widget.NewList(
		func() int {
			lv.mu.Lock()
			defer lv.mu.Unlock()
			return len(lv.lines)
		},
		func() fyne.CanvasObject {
			if lv.color {
				return newColorLine()
			}
			if !stamp {
				t := canvas.NewText("", theme.Color(theme.ColorNameForeground))
				t.TextStyle = fyne.TextStyle{Monospace: true}
				return t
			}
			l := widget.NewLabel("")
			l.TextStyle = fyne.TextStyle{Monospace: true}
			l.Truncation = fyne.TextTruncateEllipsis
			return l
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			lv.mu.Lock()
			text := ""
			if id < len(lv.lines) {
				text = lv.lines[id]
			}
			lv.mu.Unlock()
			switch w := o.(type) {
			case *colorLine:
				w.Set(text)
			case *widget.Label:
				w.SetText(stripANSI(text))
			case *canvas.Text:
				w.Text = stripANSI(text)
				w.Color = theme.Color(theme.ColorNameForeground)
				w.Refresh()
			}
		},
	)
	lv.list.HideSeparators = true
	return lv
}

func (lv *logView) add(line string) {
	lv.mu.Lock()
	if lv.stamp {
		line = time.Now().Format("15:04:05 ") + line
	}
	lv.lines = append(lv.lines, line)
	if len(lv.lines) > maxLogLines {
		extra := len(lv.lines) - maxLogLines
		lv.lines = lv.lines[extra:]
		lv.dropped += extra
	}
	schedule := !lv.pending
	lv.pending = true
	lv.mu.Unlock()

	if schedule {
		// coalesce refreshes when output is fast
		time.AfterFunc(80*time.Millisecond, func() {
			lv.mu.Lock()
			lv.pending = false
			lv.mu.Unlock()
			fyne.Do(func() { lv.refreshDoc(true) })
		})
	}
}

// setLines replaces the content (used to show a server's existing console).
func (lv *logView) setLines(lines []string) {
	lv.mu.Lock()
	lv.lines = append([]string(nil), lines...)
	lv.dropped = 0
	lv.mu.Unlock()
	if lv.doc != nil {
		lv.doc.invalidate()
	}
	lv.refreshDoc(true)
}

func (lv *logView) text() string {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	plain := make([]string, len(lv.lines))
	for i, l := range lv.lines {
		plain[i] = stripANSI(l)
	}
	return strings.Join(plain, "\n")
}

func (lv *logView) clear() {
	lv.mu.Lock()
	lv.lines = nil
	lv.dropped = 0
	lv.mu.Unlock()
	if lv.doc != nil {
		lv.doc.invalidate()
	}
	lv.refreshDoc(false)
}

func (lv *logView) refreshDoc(follow bool) {
	if lv.doc != nil {
		// Snapshot under the lock. Widths and the selection are applied to that
		// snapshot, so a line trimmed after the snapshot waits for the next refresh
		// instead of shifting already-measured rows.
		lv.mu.Lock()
		dropped := lv.dropped
		lv.dropped = 0
		lines := append([]string(nil), lv.lines...)
		lv.mu.Unlock()
		lv.doc.dropPrefix(dropped)
		lv.doc.syncLines(lines)
		lv.doc.adjusting = true
		lv.doc.Refresh()
		if lv.scroll != nil {
			lv.scroll.Refresh()
			if follow && lv.doc.stick && !lv.doc.dragging {
				lv.scroll.ScrollToBottom()
			}
		}
		lv.doc.adjusting = false
		return
	}
	lv.mu.Lock()
	lv.dropped = 0
	lv.mu.Unlock()
	lv.list.Refresh()
	if follow {
		lv.list.ScrollToBottom()
	}
}

func (lv *logView) length() int {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	return len(lv.lines)
}

func (lv *logView) lineAt(i int) (string, bool) {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	if i < 0 || i >= len(lv.lines) {
		return "", false
	}
	return lv.lines[i], true
}

func (lv *logView) copyLines() []string {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	return append([]string(nil), lv.lines...)
}

func (lv *logView) slice(first, last int) []string {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	if len(lv.lines) == 0 || first > last {
		return nil
	}
	if first < 0 {
		first = 0
	}
	if last >= len(lv.lines) {
		last = len(lv.lines) - 1
	}
	if first > last {
		return nil
	}
	return append([]string(nil), lv.lines[first:last+1]...)
}

func (lv *logView) plainRange(first, last int) []string {
	raw := lv.slice(first, last)
	for i, s := range raw {
		raw[i] = stripANSI(s)
	}
	return raw
}

// panel wraps the log in the grey console background with inner padding.
func (lv *logView) panel() fyne.CanvasObject {
	if lv.scroll != nil {
		return greyPanel(lv.scroll)
	}
	return greyPanel(lv.list)
}

func (lv *logView) object(a *App) fyne.CanvasObject {
	copyBtn := widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), func() {
		a.fa.Clipboard().SetContent(lv.text())
	})
	clearBtn := widget.NewButtonWithIcon("Clear", theme.DeleteIcon(), lv.clear)
	return container.NewBorder(container.NewHBox(copyBtn, clearBtn), nil, nil, nil, lv.panel())
}

// colorLine renders one console row with ANSI colour spans.
type colorLine struct {
	widget.BaseWidget
	raw   string
	fg    color.Color
	shown bool
	box   *fyne.Container
}

func newColorLine() *colorLine {
	// Zero padding: the default HBox inserts theme padding between colour spans,
	// which makes the glyphs wider than the selection measured from the plain text.
	c := &colorLine{box: container.New(layout.NewCustomPaddedHBoxLayout(0))}
	c.ExtendBaseWidget(c)
	// Seed a non-empty glyph so the List template reports a real row height.
	t := canvas.NewText("Ag", theme.Color(theme.ColorNameForeground))
	t.TextStyle = fyne.TextStyle{Monospace: true}
	c.box.Objects = []fyne.CanvasObject{t}
	return c
}

func colorLineHeight() float32 {
	return theme.TextSize() + theme.InnerPadding()
}

func sameColor(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

func (c *colorLine) Set(raw string) {
	fg := theme.Color(theme.ColorNameForeground)
	// Selection refreshes the visible rows on every drag. Rebuilding the text
	// objects when the line has not changed is what makes a fast drag stutter.
	if c.shown && raw == c.raw && sameColor(fg, c.fg) {
		return
	}
	c.shown = true
	c.raw = raw
	c.fg = fg
	spans := parseANSI(raw)
	objs := make([]fyne.CanvasObject, 0, len(spans))
	for _, s := range spans {
		col := s.Color
		if col == nil {
			col = fg
		}
		t := canvas.NewText(s.Text, col)
		t.TextStyle = fyne.TextStyle{Monospace: true}
		objs = append(objs, t)
	}
	if len(objs) == 0 {
		t := canvas.NewText(" ", fg)
		t.TextStyle = fyne.TextStyle{Monospace: true}
		objs = []fyne.CanvasObject{t}
	}
	c.box.Objects = objs
	c.box.Refresh()
	c.Refresh()
}

func (c *colorLine) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.box)
}

func (c *colorLine) MinSize() fyne.Size {
	h := colorLineHeight()
	w := float32(0)
	if c.box != nil {
		for _, o := range c.box.Objects {
			ms := o.MinSize()
			w += ms.Width
			h = fyne.Max(h, ms.Height)
		}
	}
	return fyne.NewSize(w, h)
}
