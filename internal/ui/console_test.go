package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

func TestSelectionText(t *testing.T) {
	lines := []string{"ab", "cd", "éf"}
	if got := selectionText(lines, 0, 1, 1, 1); got != "b\nc" {
		t.Fatalf("got %q", got)
	}
	if got := selectionText(lines, 1, 1, 0, 1); got != "b\nc" {
		t.Fatalf("reverse got %q", got)
	}
	if got := selectionText(lines, 2, 0, 2, 1); got != "é" {
		t.Fatalf("rune got %q", got)
	}
	if got := selectionText(lines, 0, 0, 2, 2); got != "ab\ncd\néf" {
		t.Fatalf("all got %q", got)
	}
}

func TestTokenBounds(t *testing.T) {
	if a, b := tokenBounds("foo bar", 1); a != 0 || b != 3 {
		t.Fatalf("word = %d,%d", a, b)
	}
	if a, b := tokenBounds("foo bar", 4); a != 4 || b != 7 {
		t.Fatalf("second = %d,%d", a, b)
	}
	if a, b := tokenBounds("foo  bar", 3); a != 3 || b != 5 {
		t.Fatalf("space = %d,%d", a, b)
	}
	if a, b := tokenBounds("", 0); a != 0 || b != 0 {
		t.Fatalf("empty = %d,%d", a, b)
	}
}

func TestConsoleDropPrefixMovesSelection(t *testing.T) {
	c := &consoleDoc{widths: []float32{5, 1, 4}, maxW: 5}
	c.sel = consoleSel{active: true, aRow: 1, aCol: 2, bRow: 2, bCol: 1}
	c.dropPrefix(1)
	if c.sel.aRow != 0 || c.sel.aCol != 2 || c.sel.bRow != 1 || c.sel.bCol != 1 {
		t.Fatalf("sel = %+v", c.sel)
	}
	if len(c.widths) != 2 || c.maxW != 4 {
		t.Fatalf("widths %v max %v", c.widths, c.maxW)
	}

	c.dropPrefix(10)
	if c.sel.active || c.maxW != 0 || c.widths != nil {
		t.Fatalf("after wipe sel=%+v widths=%v max=%v", c.sel, c.widths, c.maxW)
	}
}

func TestConsoleTrimRealignsWidths(t *testing.T) {
	test.NewApp()
	lv := newLogViewWith(false)
	lv.setLines([]string{"a", "bb", "ccc"})
	lv.doc.sel = consoleSel{active: true, aRow: 1, aCol: 0, bRow: 2, bCol: 3}

	lv.mu.Lock()
	lv.lines = []string{"bb", "ccc", "dddd"}
	lv.dropped = 1
	lv.mu.Unlock()
	lv.refreshDoc(false)

	if len(lv.doc.widths) != 3 {
		t.Fatalf("widths %v", lv.doc.widths)
	}
	if lv.doc.widths[0] != monoWidth("bb") || lv.doc.widths[2] != monoWidth("dddd") {
		t.Fatalf("widths %v", lv.doc.widths)
	}
	if got := lv.doc.selectedText(); got != "bb\nccc" {
		t.Fatalf("selection %q", got)
	}
}

func TestConsoleDragHitsDocument(t *testing.T) {
	test.NewApp()
	lv := newLogViewWith(false)
	lv.setLines([]string{"alpha beta", "gamma"})
	c := test.NewCanvas()
	c.SetPadded(false)
	c.SetContent(lv.panel())
	c.Resize(fyne.NewSize(360, 240))

	pad := theme.InnerPadding()
	lineH := consoleLineHeight()
	pos := fyne.NewPos(pad+monoWidth("alph"), pad+lineH/2)
	lv.doc.MouseDown(&desktop.MouseEvent{
		PointEvent: fyne.PointEvent{Position: fyne.NewPos(0, lineH/2)},
		Button:     desktop.MouseButtonPrimary,
	})
	test.Drag(c, pos, 12, 0)
	got := lv.doc.selectedText()
	if got == "" || !strings.Contains("alpha beta", got) {
		t.Fatalf("canvas drag selection %q", got)
	}
}

func TestConsoleDragAndCopy(t *testing.T) {
	test.NewApp()
	lv := newLogViewWith(false)
	lv.setLines([]string{"\x1b[31malpha beta\x1b[0m", "gamma"})

	lineH := consoleLineHeight()
	lv.doc.MouseDown(&desktop.MouseEvent{
		PointEvent: fyne.PointEvent{Position: fyne.NewPos(0, lineH/2)},
		Button:     desktop.MouseButtonPrimary,
	})
	lv.doc.Dragged(&fyne.DragEvent{
		PointEvent: fyne.PointEvent{Position: fyne.NewPos(monoWidth("alpha"), lineH/2)},
		Dragged:    fyne.NewDelta(monoWidth("alpha"), 0),
	})
	got := lv.doc.selectedText()
	if got == "" || !strings.Contains("alpha beta", got) {
		t.Fatalf("selection %q", got)
	}

	lv.doc.Dragged(&fyne.DragEvent{
		PointEvent: fyne.PointEvent{Position: fyne.NewPos(monoWidth("ga"), lineH*1.5)},
		Dragged:    fyne.NewDelta(0, lineH),
	})
	got = lv.doc.selectedText()
	if !strings.Contains(got, "\n") {
		t.Fatalf("expected a multi-line selection, got %q", got)
	}

	lv.doc.TypedShortcut(&fyne.ShortcutSelectAll{})
	if lv.doc.selectedText() != "alpha beta\ngamma" {
		t.Fatalf("select all %q", lv.doc.selectedText())
	}
	clip := fyne.CurrentApp().Clipboard()
	lv.doc.TypedShortcut(&fyne.ShortcutCopy{Clipboard: clip})
	if clip.Content() != "alpha beta\ngamma" {
		t.Fatalf("clipboard %q", clip.Content())
	}
}
