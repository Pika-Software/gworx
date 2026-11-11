package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Pika-Software/gworx/internal/workshop"
)

func TestDamagedLibraryIsSetAside(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "library.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Open(dir)
	if err == nil || !strings.Contains(err.Error(), "moved to") {
		t.Fatalf("expected a set-aside error, got %v", err)
	}
	if err := s.AddDownload(Download{ID: 1}); err != nil {
		t.Fatal(err)
	}

	matches, _ := filepath.Glob(path + ".bad-*")
	if len(matches) != 1 {
		t.Fatalf("backup not found: %v", matches)
	}
	if raw, _ := os.ReadFile(matches[0]); string(raw) != "{not json" {
		t.Fatalf("backup content changed: %q", raw)
	}
}

func TestUpsertKeepsVisibility(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPublished(Published{ID: 7, Title: "a", Visibility: workshop.VisibilityUnlisted}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPublished(Published{ID: 7, Visibility: workshop.VisibilityKeep}); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.GetPublished(7); p.Visibility != workshop.VisibilityUnlisted {
		t.Fatalf("visibility = %d, want unlisted", p.Visibility)
	}
}
