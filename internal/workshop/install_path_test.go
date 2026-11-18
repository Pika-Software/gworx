package workshop

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveInstallDir(t *testing.T) {
	prevRoots := steamRootsOverride
	prevHost := flatpakHostRoot
	t.Cleanup(func() {
		steamRootsOverride = prevRoots
		flatpakHostRoot = prevHost
	})
	steamRootsOverride = []string{}

	reported := "/mnt/ssd1/SteamLibrary/steamapps/workshop/content/4000/3822502910"

	t.Run("reported path", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "item")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		got, err := resolveInstallDir(dir)
		if err != nil || got != dir {
			t.Fatalf("resolveInstallDir = %q, %v; want %q", got, err, dir)
		}
	})

	t.Run("flatpak host root", func(t *testing.T) {
		host := t.TempDir()
		flatpakHostRoot = host
		dir := filepath.Join(host, "mnt/ssd1/SteamLibrary/steamapps/workshop/content/4000/3822502910")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		got, err := resolveInstallDir(reported)
		if err != nil || got != dir {
			t.Fatalf("resolveInstallDir = %q, %v; want %q", got, err, dir)
		}
	})

	t.Run("other steam library", func(t *testing.T) {
		flatpakHostRoot = t.TempDir() // nothing mounted here
		lib := t.TempDir()
		steamRootsOverride = []string{lib}
		dir := filepath.Join(lib, "steamapps", "workshop", "content", "4000", "3822502910")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		got, err := resolveInstallDir(reported)
		if err != nil || got != dir {
			t.Fatalf("resolveInstallDir = %q, %v; want %q", got, err, dir)
		}
	})

	t.Run("missing", func(t *testing.T) {
		flatpakHostRoot = t.TempDir()
		steamRootsOverride = []string{}
		_, err := resolveInstallDir(reported)
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("err = %v, want not exist", err)
		}
	})
}

func TestSteamLibraries(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "steamapps"), 0o755); err != nil {
		t.Fatal(err)
	}
	vdf := "\"libraryfolders\"\n{\n\t\"0\"\n\t{\n\t\t\"path\"\t\t\"/mnt/ssd1/SteamLibrary\"\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(root, "steamapps", "libraryfolders.vdf"), []byte(vdf), 0o644); err != nil {
		t.Fatal(err)
	}
	got := steamLibraries([]string{root})
	want := []string{root, "/mnt/ssd1/SteamLibrary"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("steamLibraries = %#v, want %#v", got, want)
	}
}

func TestFlatpakFilesystemArg(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/mnt/ssd1/SteamLibrary/steamapps/workshop/content/4000/1", "/mnt/ssd1"},
		{"/media/games/Steam/steamapps/workshop/content/4000/1", "/media/games"},
		{"/run/media/alice/Games/Steam/steamapps/workshop/content/4000/1", "/run/media/alice/Games"},
		{"/opt/steamlib/steamapps/workshop/content/4000/1", "/opt/steamlib"},
	}
	for _, tc := range cases {
		if got := flatpakFilesystemArg(tc.in); got != tc.want {
			t.Errorf("flatpakFilesystemArg(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestInstallPathError(t *testing.T) {
	prev := flatpakInfoPath
	t.Cleanup(func() { flatpakInfoPath = prev })

	missing := "/mnt/ssd1/SteamLibrary/steamapps/workshop/content/4000/1"
	err := installPathError(missing, os.ErrNotExist)
	if strings.Contains(err.Error(), "flatpak override") {
		t.Fatalf("outside flatpak, got %v", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("errors.Is = false for %v", err)
	}

	info := filepath.Join(t.TempDir(), "flatpak-info")
	if err := os.WriteFile(info, []byte("name=eu.p1ka.gworx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	flatpakInfoPath = info
	err = installPathError(missing, os.ErrNotExist)
	if !strings.Contains(err.Error(), "flatpak override --user --filesystem=/mnt/ssd1 eu.p1ka.gworx") {
		t.Fatalf("flatpak error = %v", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("errors.Is = false for %v", err)
	}
}
