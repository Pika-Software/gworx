package workshop

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestSteamCMDInDir(t *testing.T) {
	dir := t.TempDir()
	if got := SteamCMDInDir(dir); got != "" {
		t.Fatalf("empty dir: got %q", got)
	}
	path := filepath.Join(dir, "steamcmd.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := SteamCMDInDir(dir); got != path {
		t.Fatalf("got %q, want %q", got, path)
	}
}

func TestSafeJoin(t *testing.T) {
	dir := t.TempDir()
	ok, err := safeJoin(dir, "steamcmd.sh")
	if err != nil || ok != filepath.Join(dir, "steamcmd.sh") {
		t.Fatalf("ok path: %q %v", ok, err)
	}
	if _, err := safeJoin(dir, "../escape"); err == nil {
		t.Fatal("expected error for ../escape")
	}
	if _, err := safeJoin(dir, "/etc/passwd"); err == nil {
		t.Fatal("expected error for absolute path")
	}
}

func TestExtractTarGzFile(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "in.tar.gz")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("#!/bin/sh\necho ok\n")
	hdr := &tar.Header{Name: "steamcmd.sh", Mode: 0o755, Size: int64(len(body))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := extractTarGzFile(archive, out); err != nil {
		t.Fatal(err)
	}
	if SteamCMDInDir(out) == "" {
		t.Fatal("steamcmd.sh missing after extract")
	}
}

func TestExtractZipFile(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "in.zip")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("steamcmd.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("MZ")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := extractZipFile(archive, out); err != nil {
		t.Fatal(err)
	}
	if SteamCMDInDir(out) == "" {
		t.Fatal("steamcmd.exe missing after extract")
	}
}
