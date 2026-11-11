package ui

import (
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
type logView struct {
	mu      sync.Mutex
	lines   []string
	pending bool
	stamp   bool
	color   bool // parse ANSI SGR colours (server console)
	list    *widget.List
}

func newLogView() *logView {
	return newLogViewWith(true)
}

// newLogViewWith creates a log view; stamp prefixes lines with the time.
func newLogViewWith(stamp bool) *logView {
	lv := &logView{stamp: stamp, color: !stamp}
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
		lv.lines = lv.lines[len(lv.lines)-maxLogLines:]
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
			fyne.Do(func() {
				lv.list.Refresh()
				lv.list.ScrollToBottom()
			})
		})
	}
}

// setLines replaces the content (used to show a server's existing console).
func (lv *logView) setLines(lines []string) {
	lv.mu.Lock()
	lv.lines = append([]string(nil), lines...)
	lv.mu.Unlock()
	lv.list.Refresh()
	lv.list.ScrollToBottom()
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
	lv.mu.Unlock()
	lv.list.Refresh()
}

// panel wraps the list in the grey console background with inner padding.
func (lv *logView) panel() fyne.CanvasObject {
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
	raw string
	box *fyne.Container
}

func newColorLine() *colorLine {
	c := &colorLine{box: container.New(layout.NewHBoxLayout())}
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

func (c *colorLine) Set(raw string) {
	c.raw = raw
	spans := parseANSI(raw)
	objs := make([]fyne.CanvasObject, 0, len(spans))
	fg := theme.Color(theme.ColorNameForeground)
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

func (c *colorLine) TappedSecondary(ev *fyne.PointEvent) {
	text := stripANSI(c.raw)
	if strings.TrimSpace(text) == "" {
		return
	}
	app := fyne.CurrentApp()
	can := app.Driver().CanvasForObject(c)
	if can == nil {
		return
	}
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", fyne.NewMenuItem("Copy", func() {
		app.Clipboard().SetContent(text)
	})), can, ev.AbsolutePosition)
}

var _ fyne.SecondaryTappable = (*colorLine)(nil)
