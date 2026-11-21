package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestDescShowsFullText(t *testing.T) {
	test.NewApp()
	d := newDescBox()
	// Longer than the old 800/1500 rune caps, no spaces, and a Steam-style CRLF.
	body := strings.Repeat("描述文字", 300) + "\r\nконец"
	d.Set(body)

	got := d.view.label.Text
	if strings.Contains(got, "\r") {
		t.Fatalf("carriage return kept: %q", got[len(got)-20:])
	}
	if !strings.HasSuffix(got, "конец") {
		t.Fatalf("tail missing: %q", got[len(got)-20:])
	}
	want := strings.ReplaceAll(body, "\r\n", "\n")
	if utf8.RuneCountInString(got) != utf8.RuneCountInString(want) {
		t.Fatalf("shown %d runes, want %d", utf8.RuneCountInString(got), utf8.RuneCountInString(want))
	}
	if !d.view.label.Selectable {
		t.Fatal("description is not selectable")
	}

	d.view.Resize(fyne.NewSize(160, 80))
	if h := d.view.label.MinSize().Height; h < 400 {
		t.Fatalf("wrapped height = %v, unspaced text did not wrap", h)
	}
}

func TestNormalizeDescKeepsPartialRune(t *testing.T) {
	// Steam's 8000-byte description cap splits a multibyte character.
	got := normalizeDesc("привет\xe6\x8fещё")
	if !utf8.ValidString(got) {
		t.Fatalf("invalid utf-8: %q", got)
	}
	if !strings.Contains(got, "привет") || !strings.Contains(got, "ещё") {
		t.Fatalf("lost text around the bad sequence: %q", got)
	}
}
