package workshop

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type archiveEntry struct{ name, body string }

func writeTarGz(t *testing.T, path string, entries []archiveEntry) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeZip(t *testing.T, path string, entries []archiveEntry) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExtractArchives(t *testing.T) {
	formats := []struct {
		name    string
		write   func(*testing.T, string, []archiveEntry)
		extract func(string, string) error
	}{
		{"tar.gz", writeTarGz, extractTarGzFile},
		{"zip", writeZip, extractZipFile},
	}

	unsafe := []string{"../escape", "linux32/../../escape"}
	if runtime.GOOS != "windows" {
		unsafe = append(unsafe, "/tmp/escape")
	}

	for _, f := range formats {
		t.Run(f.name, func(t *testing.T) {
			dir := t.TempDir()
			archive := filepath.Join(dir, "in."+f.name)
			out := filepath.Join(dir, "out")
			f.write(t, archive, []archiveEntry{
				{"steamcmd.sh", "#!/bin/sh\n"},
				{"linux32/steamcmd", "ELF"},
			})
			if err := f.extract(archive, out); err != nil {
				t.Fatal(err)
			}
			if got := SteamCMDInDir(out); got != filepath.Join(out, "steamcmd.sh") {
				t.Errorf("SteamCMDInDir = %q", got)
			}
			if b, err := os.ReadFile(filepath.Join(out, "linux32", "steamcmd")); err != nil || string(b) != "ELF" {
				t.Errorf("nested file = %q, %v", b, err)
			}

			for _, name := range unsafe {
				out := filepath.Join(dir, "unsafe", "out")
				f.write(t, archive, []archiveEntry{{name, "x"}})
				if err := f.extract(archive, out); err == nil {
					t.Errorf("%q: expected an error", name)
				}
				if _, err := os.Stat(filepath.Join(dir, "unsafe", "escape")); !os.IsNotExist(err) {
					t.Errorf("%q escaped the destination", name)
				}
			}
		})
	}
}
