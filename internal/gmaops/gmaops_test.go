package gmaops

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ulikunitz/xz/lzma"
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

// buildGMA builds a one-file addon containing lua/<name> and returns its bytes and path.
func buildGMA(t *testing.T, name, content string) (string, []byte) {
	t.Helper()
	src := t.TempDir()
	write(t, src, "lua/"+name, content)
	p := &Project{Title: "Test", Version: 1, Category: "tool", Tags: []string{"fun"}, SourceDir: src}
	out := filepath.Join(t.TempDir(), "test.gma")
	if _, err := Build(p, out, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return out, raw
}

// patch replaces old with new (same length) in the GMA at path.
func patch(t *testing.T, path string, raw []byte, old, new string) {
	t.Helper()
	if len(old) != len(new) {
		t.Fatalf("patch must keep the length: %q -> %q", old, new)
	}
	i := bytes.Index(raw, []byte(old))
	if i < 0 {
		t.Fatalf("%q not found in GMA", old)
	}
	raw = bytes.Clone(raw)
	copy(raw[i:], new)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildExtractRoundTrip(t *testing.T) {
	src := t.TempDir()
	write(t, src, "lua/autorun/test.lua", "print('hi')")
	write(t, src, "Materials/Test/Thing.vmt", "\"VertexLitGeneric\" {}")
	write(t, src, "lua/notes.txt", "ignored by wildcard")
	write(t, src, "Lua/README.TXT", "ignored by wildcard, case-insensitively")
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
		Ignore:          []string{"*.txt", ".git/", "app.exe"},
	}

	out := filepath.Join(t.TempDir(), "out.gma")
	scan, err := Build(p, out, nil)
	if err != nil {
		t.Fatal(err)
	}
	var inc []string
	for _, e := range scan.Included {
		inc = append(inc, e.Internal)
	}
	slices.Sort(inc)
	if want := []string{"lua/autorun/test.lua", "materials/test/thing.vmt"}; !slices.Equal(inc, want) {
		t.Errorf("included = %v, want %v", inc, want)
	}
	if !slices.Equal(scan.Disallowed, []string{"random.bin"}) {
		t.Errorf("disallowed = %v", scan.Disallowed)
	}
	slices.Sort(scan.Ignored)
	if want := []string{".git/", "Lua/README.TXT", "app.exe", "lua/notes.txt"}; !slices.Equal(scan.Ignored, want) {
		t.Errorf("ignored = %v, want %v", scan.Ignored, want)
	}

	inf, err := Inspect(out)
	if err != nil {
		t.Fatal(err)
	}
	if inf.Title != "Round Trip" || inf.Author != "tester" || inf.Version != 3 || inf.Legacy {
		t.Errorf("metadata = %+v", inf)
	}
	if !slices.Equal(inf.RequiredContent, []string{"104603291"}) {
		t.Errorf("required = %v", inf.RequiredContent)
	}

	dest := t.TempDir()
	n, err := Extract(out, dest, ExtractOptions{VerifyCRC: true, Warn: func(s string) { t.Error(s) }})
	if err != nil || n != 2 {
		t.Fatalf("extract n=%d err=%v", n, err)
	}
	for rel, want := range map[string]string{
		"lua/autorun/test.lua":     "print('hi')",
		"materials/test/thing.vmt": "\"VertexLitGeneric\" {}",
	} {
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(rel)))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v", rel, got, err)
		}
	}
}

func TestBuildFailsWithoutAllowedFiles(t *testing.T) {
	src := t.TempDir()
	write(t, src, "random.bin", "not whitelisted")
	p := &Project{Title: "x", Version: 1, Category: "tool", Tags: []string{"fun"}, SourceDir: src}
	out := filepath.Join(t.TempDir(), "out.gma")
	if _, err := Build(p, out, nil); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("output must not be created: %v", err)
	}
}

func TestValidate(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	write(t, filepath.Dir(file), "file", "")
	valid := func() *Project {
		return &Project{Title: "x", Version: 1, Category: "tool", Tags: []string{"fun"}, SourceDir: t.TempDir()}
	}
	if err := valid().Validate(); err != nil {
		t.Fatalf("baseline project must be valid: %v", err)
	}
	for name, mod := range map[string]func(*Project){
		"blank title":    func(p *Project) { p.Title = "  " },
		"version 0":      func(p *Project) { p.Version = 0 },
		"no folder":      func(p *Project) { p.SourceDir = "" },
		"missing folder": func(p *Project) { p.SourceDir = filepath.Join(p.SourceDir, "nope") },
		"file as folder": func(p *Project) { p.SourceDir = file },
		"bad category":   func(p *Project) { p.Category = "nope" },
		"no tags":        func(p *Project) { p.Tags = nil },
		"duplicate tags": func(p *Project) { p.Tags = []string{"fun", "fun"} },
		"unknown tag":    func(p *Project) { p.Tags = []string{"nope"} },
	} {
		p := valid()
		mod(p)
		if p.Validate() == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

func TestAddonJSONRoundTrip(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "addon.json", `{"title": "T", "type": "ServerContent", "tags": ["Fun", "build"], "ignore": ["*.psd"]}`)
	p, err := LoadAddonJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Category != "servercontent" || !slices.Equal(p.Tags, []string{"fun", "build"}) || p.SourceDir != dir {
		t.Fatalf("loaded %+v", p)
	}

	p.Author, p.Version, p.RequiredContent = "me", 2, []string{"1"}
	if err := SaveAddonJSON(p); err != nil {
		t.Fatal(err)
	}
	back, err := LoadAddonJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	if back.Title != "T" || back.Author != "me" || back.Version != 2 ||
		!slices.Equal(back.Ignore, []string{"*.psd"}) || !slices.Equal(back.RequiredContent, []string{"1"}) {
		t.Fatalf("round trip = %+v", back)
	}
}

func TestVerifyDetectsCorruption(t *testing.T) {
	path, raw := buildGMA(t, "a.lua", "return 1")
	if res, err := Verify(path, nil); err != nil || res.Bad != 0 {
		t.Fatalf("intact addon: %+v %v", res, err)
	}

	patch(t, path, raw, "return 1", "return 2")
	res, err := Verify(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Bad != 1 || res.OK[0] {
		t.Fatalf("corrupted addon: %+v", res)
	}

	var warnings []string
	n, err := Extract(path, t.TempDir(), ExtractOptions{VerifyCRC: true, Warn: func(s string) { warnings = append(warnings, s) }})
	if err != nil || n != 1 || len(warnings) != 1 || !strings.Contains(warnings[0], "checksum") {
		t.Fatalf("extract n=%d err=%v warnings=%q", n, err, warnings)
	}
}

// Workshop addons are untrusted: a crafted file table must not write outside dest.
func TestExtractSkipsUnsafePaths(t *testing.T) {
	path, raw := buildGMA(t, "a.lua", "return 1")
	patch(t, path, raw, "lua/a.lua", "..//a.lua")

	root := t.TempDir()
	dest := filepath.Join(root, "dest")
	var warnings []string
	n, err := Extract(path, dest, ExtractOptions{Warn: func(s string) { warnings = append(warnings, s) }})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || len(warnings) != 1 || !strings.Contains(warnings[0], "unsafe") {
		t.Errorf("n=%d warnings=%q", n, warnings)
	}
	if _, err := os.Stat(filepath.Join(root, "a.lua")); !os.IsNotExist(err) {
		t.Errorf("file escaped the destination: %v", err)
	}
}

func TestNormalizeLegacyLZMA(t *testing.T) {
	plain, raw := buildGMA(t, "a.lua", "return 1")
	dir := filepath.Dir(plain)

	legacy := filepath.Join(dir, "item_legacy.bin")
	f, err := os.Create(legacy)
	if err != nil {
		t.Fatal(err)
	}
	w, err := lzma.WriterConfig{DictCap: 1 << 24}.NewWriter(f) // Valve legacy files: header starts 5D 00 00 00
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	out := filepath.Join(dir, "out", "123.gma")
	inf, err := Normalize(legacy, out)
	if err != nil {
		t.Fatal(err)
	}
	if inf.Title != "Test" || inf.Legacy {
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
