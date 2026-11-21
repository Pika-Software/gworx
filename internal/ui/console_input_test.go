package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
)

func TestCmdHistWalksPrefix(t *testing.T) {
	h := &cmdHist{items: []string{"status", "changelevel gm_flatgrass", "maps"}, browse: -1}
	h.edit("c")
	got, ok := h.up()
	if !ok || got != "changelevel gm_flatgrass" {
		t.Fatalf("up = %q %v", got, ok)
	}
	if _, ok := h.up(); ok {
		t.Fatal("older prefix match")
	}
	got, ok = h.down()
	if !ok || got != "c" {
		t.Fatalf("down = %q %v", got, ok)
	}
	if h.browse >= 0 {
		t.Fatal("down past the end should leave history")
	}
}

func TestCmdHistRecentLast(t *testing.T) {
	h := &cmdHist{browse: -1}
	h.push("status")
	h.push("maps")
	h.push("status")
	if !slices.Equal(h.items, []string{"maps", "status"}) {
		t.Fatalf("items %q", h.items)
	}
	h.edit("")
	got, ok := h.up()
	if !ok || got != "status" {
		t.Fatalf("up = %q", got)
	}
	got, ok = h.up()
	if !ok || got != "maps" {
		t.Fatalf("older = %q", got)
	}
	if _, ok := h.cancel(); !ok {
		t.Fatal("cancel")
	}
}

func TestSuggestCommands(t *testing.T) {
	hist := []string{"status", "changelevel gm_flatgrass"}
	got := suggestCommands("cha", hist, nil)
	if len(got) == 0 || got[0] != "changelevel gm_flatgrass" {
		t.Fatalf("history first: %q", got)
	}
	if !slices.Contains(got, "changelevel") {
		t.Fatalf("catalog missing: %q", got)
	}

	got = suggestCommands("sta", nil, nil)
	if len(got) < 2 || got[0] != "status" || got[1] != "stats" {
		t.Fatalf("catalog order %q", got)
	}

	got = suggestCommands("changelevel gm_f", nil, nil)
	if len(got) != 1 || got[0] != "changelevel gm_flatgrass" {
		t.Fatalf("map arg %q", got)
	}
	if suggestCommands("status", nil, nil) != nil {
		t.Fatal("exact command should not suggest itself")
	}

	got = suggestCommands("zz_", nil, []string{"zz_custom"})
	if len(got) != 1 || got[0] != "zz_custom" {
		t.Fatalf("learned %q", got)
	}
	if suggestCommands("   ", hist, nil) != nil {
		t.Fatal("blank line")
	}
}

func TestParseCvarLine(t *testing.T) {
	if got := parseCvarLine(`sv_cheats                          : 0        : , "nf" : Allow cheats`); got != "sv_cheats" {
		t.Fatalf("cvarlist %q", got)
	}
	if got := parseCvarLine(`"hostname" = "My Server"`); got != "hostname" {
		t.Fatalf("assign %q", got)
	}
	for _, line := range []string{
		`* Server stopped`,
		`L 11/21/2026 - 12:00:00: "Player<2>" say "hi"`,
		`Error: something: bad`,
		`Players: 1 humans, 0 bots`,
	} {
		if got := parseCvarLine(line); got != "" {
			t.Fatalf("%q -> %q", line, got)
		}
	}
}

func TestConsoleHistoryStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console-history.json")
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if items := loadConsoleHistory(path).list("a"); items != nil {
		t.Fatalf("corrupt file returned %q", items)
	}

	s := loadConsoleHistory(path)
	s.put("srv", []string{"status", "maps"})
	got := loadConsoleHistory(path).list("srv")
	if !slices.Equal(got, []string{"status", "maps"}) {
		t.Fatalf("reloaded %q", got)
	}
}

func TestConsoleEntryHistoryAndTab(t *testing.T) {
	test.NewApp()
	e := newConsoleEntry(nil)
	e.hist.items = []string{"status", "changelevel gm_flatgrass", "maps"}
	e.SetText("c")
	e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyUp})
	if e.Text != "changelevel gm_flatgrass" {
		t.Fatalf("up %q", e.Text)
	}
	e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	if e.Text != "c" {
		t.Fatalf("down %q", e.Text)
	}

	e.SetText("sta")
	e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyTab})
	if e.Text != "status" {
		t.Fatalf("tab %q", e.Text)
	}
	if e.CursorColumn != len("status") {
		t.Fatalf("cursor %d", e.CursorColumn)
	}
	e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyTab})
	if e.Text != "stats" {
		t.Fatalf("second tab %q", e.Text)
	}
	e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if e.Text != "sta" {
		t.Fatalf("escape %q", e.Text)
	}
	if !e.AcceptsTab() {
		t.Fatal("tab should stay in the command box")
	}
}

func TestConsoleEntryLimitsLength(t *testing.T) {
	test.NewApp()
	e := newConsoleEntry(nil)
	e.SetText(strings.Repeat("я", maxConsoleCommand+8))
	if got := utf8.RuneCountInString(e.Text); got != maxConsoleCommand {
		t.Fatalf("length %d, want %d", got, maxConsoleCommand)
	}
	if e.Text != strings.Repeat("я", maxConsoleCommand) {
		t.Fatal("command was not cut on a rune boundary")
	}
}

func TestSuggestListPick(t *testing.T) {
	test.NewApp()
	var picked string
	s := newSuggestList(func(text string) { picked = text })
	s.set([]string{"status", "stats"}, 0)
	s.MouseDown(&desktop.MouseEvent{
		PointEvent: fyne.PointEvent{Position: fyne.NewPos(4, s.rowH()*1.5)},
		Button:     desktop.MouseButtonPrimary,
	})
	if picked != "stats" {
		t.Fatalf("picked %q", picked)
	}
}
