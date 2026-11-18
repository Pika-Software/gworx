package ui

import (
	"math"
	"strings"
	"testing"
	"time"

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
	c := &consoleDoc{metrics: []lineMetric{{width: 5}, {width: 1}, {width: 4}}, maxW: 5}
	c.sel = consoleSel{active: true, aRow: 1, aCol: 2, bRow: 2, bCol: 1}
	c.dropPrefix(1)
	if c.sel.aRow != 0 || c.sel.aCol != 2 || c.sel.bRow != 1 || c.sel.bCol != 1 {
		t.Fatalf("sel = %+v", c.sel)
	}
	if len(c.metrics) != 2 || c.maxW != 4 {
		t.Fatalf("metrics %v max %v", c.metrics, c.maxW)
	}

	c.dropPrefix(10)
	if c.sel.active || c.maxW != 0 || c.metrics != nil {
		t.Fatalf("after wipe sel=%+v metrics=%v max=%v", c.sel, c.metrics, c.maxW)
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

	if len(lv.doc.metrics) != 3 {
		t.Fatalf("metrics %v", lv.doc.metrics)
	}
	if lv.doc.metrics[0].width != monoWidth("bb") || lv.doc.metrics[2].width != monoWidth("dddd") {
		t.Fatalf("metrics %v", lv.doc.metrics)
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

func TestColorSpansHaveNoGap(t *testing.T) {
	test.NewApp()
	raw := "\x1b[31mabcd\x1b[32mefgh\x1b[0m"
	line := newColorLine()
	line.Set(raw)
	line.Resize(line.MinSize())
	if len(line.box.Objects) != 2 {
		t.Fatalf("spans %d", len(line.box.Objects))
	}
	gap := line.box.Objects[1].Position().X - line.box.Objects[0].Size().Width
	if math.Abs(float64(gap)) > 0.5 {
		t.Fatalf("gap %v between colour spans", gap)
	}
	first := line.box.Objects[0]
	line.Set(raw)
	if line.box.Objects[0] != first {
		t.Fatal("Set rebuilt unchanged colour spans")
	}
}

func TestSelectionXMatchesSpans(t *testing.T) {
	test.NewApp()
	raw := "\x1b[31mabcd\x1b[32mefgh\x1b[0m"
	lv := newLogViewWith(false)
	lv.setLines([]string{raw, "tail"})
	lv.doc.ensureCuts(0)
	m := lv.doc.metrics[0]
	if len(m.spans) != 2 || m.runes() != 8 {
		t.Fatalf("metric %+v runes %d", m.spans, m.runes())
	}
	if math.Abs(float64(m.xAt(4)-monoWidth("abcd"))) > 0.1 {
		t.Fatalf("span boundary %v", m.xAt(4))
	}
	want6 := monoWidth("abcd") + monoWidth("ef")
	if math.Abs(float64(m.xAt(6)-want6)) > 0.1 {
		t.Fatalf("second span %v want %v", m.xAt(6), want6)
	}
	for i := 0; i <= m.runes(); i++ {
		if got := m.colAt(m.xAt(i)); got != i {
			t.Fatalf("col %d -> %d", i, got)
		}
	}

	lineH := consoleLineHeight()
	lv.doc.MouseDown(&desktop.MouseEvent{
		PointEvent: fyne.PointEvent{Position: fyne.NewPos(0, lineH/2)},
		Button:     desktop.MouseButtonPrimary,
	})
	lv.doc.Dragged(&fyne.DragEvent{
		PointEvent: fyne.PointEvent{Position: fyne.NewPos(m.xAt(6), lineH/2)},
	})
	if got := lv.doc.selectedText(); got != "abcdef" {
		t.Fatalf("selection %q", got)
	}
}

func TestFastDragTracksPointer(t *testing.T) {
	test.NewApp()
	lv := newLogViewWith(false)
	line := strings.Repeat("abcdefghijklmnopqrstuvwxyz", 4)
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = line
	}
	lv.setLines(lines)
	c := test.NewCanvas()
	c.SetPadded(false)
	c.SetContent(lv.panel())
	c.Resize(fyne.NewSize(900, 700))

	lineH := consoleLineHeight()
	lv.doc.MouseDown(&desktop.MouseEvent{
		PointEvent: fyne.PointEvent{Position: fyne.NewPos(0, lineH/2)},
		Button:     desktop.MouseButtonPrimary,
	})
	const steps = 80
	lv.doc.ensureCuts(0)
	start := time.Now()
	for i := 1; i <= steps; i++ {
		lv.doc.Dragged(&fyne.DragEvent{
			PointEvent: fyne.PointEvent{Position: fyne.NewPos(lv.doc.metrics[0].xAt(i), lineH/2)},
		})
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("drag took %s", elapsed)
	}
	if got := lv.doc.selectedText(); got != line[:steps] {
		t.Fatalf("selection %q", got)
	}
}
