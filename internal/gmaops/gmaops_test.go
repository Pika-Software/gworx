package gmaops

import (
	"bytes"

	"github.com/ulikunitz/xz/lzma"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildExtractRoundTrip(t *testing.T) {
	src := t.TempDir()
	write(t, src, "lua/autorun/test.lua", "print('hi')")
	write(t, src, "Materials/Test/Thing.vmt", "\"VertexLitGeneric\" {}")
	write(t, src, "lua/notes.txt", "ignored by wildcard")
	write(t, src, ".git/config", "ignored dir")
	write(t, src, "app.exe", "ignored by name")
	write(t, src, "random.bin", "not whitelisted")

	p := &Project{
		Title:           "Round Trip",
		Author:          "tester",
		Version:         3,
		Category:        "tool",
		Tags:            []string{"fun", "build"},
		RequiredContent: []string{"104603291"},
		SourceDir:       src,
		Ignore:          []string{"*.txt", ".git", "app.exe"},
	}

	scan, err := Scan(p)
	if err != nil {
		t.Fatal(err)
	}
	var inc []string
	for _, e := range scan.Included {
		inc = append(inc, e.Internal)
	}
	slices.Sort(inc)
	if want := []string{"lua/autorun/test.lua", "materials/test/thing.vmt"}; !slices.Equal(inc, want) {
		t.Fatalf("included = %v, want %v", inc, want)
	}
	if !slices.Equal(scan.Disallowed, []string{"random.bin"}) {
		t.Fatalf("disallowed = %v", scan.Disallowed)
	}
	if len(scan.Ignored) != 3 {
		t.Fatalf("ignored = %v", scan.Ignored)
	}

	out := filepath.Join(t.TempDir(), "out.gma")
	if _, err := Build(p, out, nil); err != nil {
		t.Fatal(err)
	}

	inf, err := Inspect(out)
	if err != nil {
		t.Fatal(err)
	}
	if inf.Title != "Round Trip" || inf.Author != "tester" || inf.Version != 3 {
		t.Fatalf("metadata = %+v", inf)
	}
	if !slices.Equal(inf.RequiredContent, []string{"104603291"}) {
		t.Fatalf("required = %v", inf.RequiredContent)
	}

	dest := t.TempDir()
	n, err := Extract(out, dest, ExtractOptions{VerifyCRC: true, Warn: func(s string) { t.Error(s) }})
	if err != nil || n != 2 {
		t.Fatalf("extract n=%d err=%v", n, err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "lua", "autorun", "test.lua"))
	if err != nil || string(got) != "print('hi')" {
		t.Fatalf("extracted content = %q, %v", got, err)
	}
}

func TestValidateRejectsBadTags(t *testing.T) {
	p := &Project{Title: "x", Version: 1, Category: "tool", Tags: []string{"fun", "fun"}, SourceDir: t.TempDir()}
	if err := p.Validate(); err == nil {
		t.Fatal("expected duplicate tag error")
	}
	p.Tags = nil
	if err := p.Validate(); err == nil {
		t.Fatal("expected missing tag error")
	}
}

func TestNormalizeLegacyLZMA(t *testing.T) {
	src := t.TempDir()
	write(t, src, "lua/autorun/a.lua", "return 1")
	p := &Project{Title: "Legacy", Version: 1, Category: "tool", Tags: []string{"fun"}, SourceDir: src}

	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.gma")
	if _, err := Build(p, plain, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}

	legacy := filepath.Join(dir, "item_legacy.bin")
	f, err := os.Create(legacy)
	if err != nil {
		t.Fatal(err)
	}
	w, err := lzma.WriterConfig{DictCap: 1 << 24}.NewWriter(f) // Valve legacy files: header starts 5D 00 00 00
	if err != nil {
		t.Fatal(err)
	}
	w.Write(raw)
	w.Close()
	f.Close()

	out := filepath.Join(dir, "out", "123.gma")
	inf, err := Normalize(legacy, out)
	if err != nil {
		t.Fatal(err)
	}
	if inf.Title != "Legacy" {
		t.Fatalf("info = %+v", inf)
	}
	got, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("normalized file differs (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "item_legacy_gworx.gma")); !os.IsNotExist(err) {
		t.Fatalf("temporary decompressed copy left behind: %v", err)
	}
}
