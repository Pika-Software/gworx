package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestCappedVScrollIgnoresWrappedHeight(t *testing.T) {
	test.NewApp()
	label := wrapLabel(strings.Repeat("description word ", 80) + strings.Repeat("a", 180))
	label.Resize(fyne.NewSize(120, 40))
	label.Refresh()
	if label.MinSize().Height < 1000 {
		t.Fatalf("narrow label height = %v, want a tall wrap", label.MinSize().Height)
	}

	s := newCappedVScroll(label, 240, 200)
	got := s.MinSize()
	if got.Width < 240 || got.Height > 200.5 {
		t.Fatalf("capped min size = %v", got)
	}
}

func TestCappedVScrollShrinksForShortText(t *testing.T) {
	test.NewApp()
	label := wrapLabel("Hi")
	label.Resize(fyne.NewSize(400, 40))
	label.Refresh()
	s := newCappedVScroll(label, 240, 200)
	if h := s.MinSize().Height; h >= 200 || h < 1 {
		t.Fatalf("short text height = %v", h)
	}
}
