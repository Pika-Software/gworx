//go:build linux

package ui

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenExternalUsesCleanEnv(t *testing.T) {
	bin := t.TempDir()
	out := filepath.Join(t.TempDir(), "out")
	body := "#!/bin/sh\n" +
		"printf '%s|%s\\n' \"$1\" \"${LD_LIBRARY_PATH-unset}\" > \"$GWORX_OPEN_OUT\"\n"
	if err := os.WriteFile(filepath.Join(bin, "xdg-open"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LD_LIBRARY_PATH", "/nix/store/libc/lib")
	t.Setenv("GWORX_OPEN_OUT", out)

	u := &url.URL{Scheme: "https", Host: "steamcommunity.com", Path: "/sharedfiles/filedetails/", RawQuery: "id=1"}
	if err := (&App{}).openExternal(u); err != nil {
		t.Fatal(err)
	}

	var got []byte
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		var err error
		if got, err = os.ReadFile(out); err == nil {
			break
		}
	}
	if want := u.String() + "|unset"; strings.TrimSpace(string(got)) != want {
		t.Fatalf("xdg-open saw %q, want %q", got, want)
	}
}

func TestOpenExternalMissingXdgOpen(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := (&App{}).openExternal(&url.URL{Scheme: "file", Path: "/tmp"}); err == nil {
		t.Fatal("expected an error without xdg-open")
	}
}
