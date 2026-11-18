//go:build linux

package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostDialogArgs(t *testing.T) {
	dir := t.TempDir()
	file := kdialogArgs(fileRequest{title: "Select file", start: dir, exts: []string{".gma"}})
	if strings.Join(file, "\x00") != strings.Join([]string{
		"--title", "Select file", "--getopenfilename", dir, "*.gma *.GMA|*.gma *.GMA",
	}, "\x00") {
		t.Fatalf("kdialog file %#v", file)
	}
	folder := kdialogArgs(fileRequest{title: "Select folder", start: dir, folder: true, exts: []string{".gma"}})
	if strings.Join(folder, "\x00") != strings.Join([]string{
		"--title", "Select folder", "--getexistingdirectory", dir,
	}, "\x00") {
		t.Fatalf("kdialog folder %#v", folder)
	}

	z := zenityArgs(fileRequest{title: "Select file", start: dir, exts: []string{".gma"}})
	want := []string{
		"--file-selection", "--title=Select file", "--filename=" + dir + string(os.PathSeparator),
		"--file-filter=*.gma *.GMA|*.gma *.GMA",
	}
	if strings.Join(z, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("zenity %#v", z)
	}
}

func TestFilterHostEnvDropsAppImageLibs(t *testing.T) {
	got := filterHostEnv([]string{
		"PATH=/usr/bin",
		"LD_LIBRARY_PATH=/nix/store/libc/lib",
		"LD_PRELOAD=/tmp/x.so",
		"LIBGL_DRIVERS_PATH=/usr/lib/dri",
		"__EGL_VENDOR_LIBRARY_DIRS=/usr/share/glvnd",
		"HOME=/home/dev",
		"WAYLAND_DISPLAY=wayland-1",
	})
	text := strings.Join(got, "\n")
	if strings.Contains(text, "LD_LIBRARY_PATH") || strings.Contains(text, "LD_PRELOAD") ||
		strings.Contains(text, "LIBGL_DRIVERS_PATH") || strings.Contains(text, "EGL_VENDOR") {
		t.Fatalf("env %q", text)
	}
	if !strings.Contains(text, "WAYLAND_DISPLAY=wayland-1") || !strings.Contains(text, "HOME=/home/dev") {
		t.Fatalf("env %q", text)
	}
}

func TestHostToolPickUsesCleanEnv(t *testing.T) {
	bin := t.TempDir()
	script := filepath.Join(bin, "kdialog")
	body := "#!/bin/sh\n" +
		"if [ -n \"$LD_LIBRARY_PATH\" ]; then echo DIRTY; exit 0; fi\n" +
		"printf '%s\\n' \"$1\" > \"$GWORX_DIALOG_ARGS\"\n" +
		"echo /picked/addon.gma\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("PATH", bin)
	t.Setenv("LD_LIBRARY_PATH", "/nix/store/libc/lib")
	t.Setenv("GWORX_DIALOG_ARGS", argsFile)

	got, err := hostToolPick(fileRequest{title: "Select file", start: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if got != "/picked/addon.gma" {
		t.Fatalf("path %q", got)
	}
	recorded, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(recorded)) != "--title" {
		t.Fatalf("argv %q", recorded)
	}
}

func TestHostToolPickCancel(t *testing.T) {
	bin := t.TempDir()
	script := filepath.Join(bin, "kdialog")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	got, err := hostToolPick(fileRequest{title: "Select file"})
	if err != nil || got != "" {
		t.Fatalf("cancel path %q err %v", got, err)
	}
}
