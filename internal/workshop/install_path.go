package workshop

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Flatpak hides host paths that the manifest does not grant. Steam still
// reports those paths (a library on another disk), so stat fails with
// "no such file or directory" even though the addon is installed.
var (
	flatpakInfoPath = "/.flatpak-info"
	flatpakHostRoot = "/run/host/root"
)

// steamRootsOverride, when non-nil, replaces the discovered Steam client
// directories. Tests set it; production leaves it nil.
var steamRootsOverride []string

var libraryPathRe = regexp.MustCompile(`"path"\s+"([^"]+)"`)

func inFlatpak() bool {
	st, err := os.Stat(flatpakInfoPath)
	return err == nil && !st.IsDir()
}

func steamClientRoots() []string {
	if steamRootsOverride != nil {
		return steamRootsOverride
	}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		return []string{`C:\Program Files (x86)\Steam`, `C:\Program Files\Steam`}
	case "darwin":
		return []string{filepath.Join(home, "Library", "Application Support", "Steam")}
	default:
		return []string{
			filepath.Join(home, ".local", "share", "Steam"),
			filepath.Join(home, ".steam", "steam"),
			filepath.Join(home, ".steam", "root"),
			filepath.Join(home, ".var", "app", "com.valvesoftware.Steam", ".local", "share", "Steam"),
		}
	}
}

// steamLibraries returns Steam client roots plus every library folder listed
// in their libraryfolders.vdf. Paths are not checked for existence.
func steamLibraries(roots []string) []string {
	var libs []string
	seen := map[string]bool{}
	add := func(p string) {
		p = filepath.Clean(p)
		if p == "" || p == "." || seen[p] {
			return
		}
		seen[p] = true
		libs = append(libs, p)
	}
	for _, root := range roots {
		add(root)
		raw, err := os.ReadFile(filepath.Join(root, "steamapps", "libraryfolders.vdf"))
		if err != nil {
			continue
		}
		for _, m := range libraryPathRe.FindAllStringSubmatch(string(raw), -1) {
			add(vdfPath(m[1]))
		}
	}
	return libs
}

func vdfPath(s string) string {
	s = string(bytes.ReplaceAll([]byte(s), []byte(`\\`), []byte(`\`)))
	return filepath.FromSlash(s)
}

// workshopRelative is the path under a Steam library
// (steamapps/workshop/content/4000/<id>). Empty when reported is not one.
func workshopRelative(reported string) string {
	slash := filepath.ToSlash(reported)
	marker := "/steamapps/workshop/content/"
	i := strings.Index(strings.ToLower(slash), marker)
	if i < 0 {
		return ""
	}
	return filepath.FromSlash(slash[i+1:])
}

func installDirCandidates(reported string) []string {
	reported = filepath.Clean(reported)
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" {
			return
		}
		p = filepath.Clean(p)
		if seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	add(reported)
	// --filesystem=host also exposes the host tree at /run/host/root.
	// filepath.Join would drop the prefix because reported is absolute.
	if strings.HasPrefix(reported, "/") && flatpakHostRoot != "" && !strings.HasPrefix(reported, flatpakHostRoot+"/") {
		add(flatpakHostRoot + reported)
	}
	if rel := workshopRelative(reported); rel != "" {
		for _, lib := range steamLibraries(steamClientRoots()) {
			add(filepath.Join(lib, rel))
		}
	}
	return out
}

// resolveInstallDir returns a workshop folder this process can read.
// Steam's path wins when it exists; otherwise a Flatpak host mount or another
// library that holds the same item.
func resolveInstallDir(reported string) (string, error) {
	var firstErr error
	for _, c := range installDirCandidates(reported) {
		st, err := os.Stat(c)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !st.IsDir() {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s is not a directory", c)
			}
			continue
		}
		return c, nil
	}
	if firstErr == nil {
		firstErr = os.ErrNotExist
	}
	return "", firstErr
}

// flatpakFilesystemArg is the directory to pass to `flatpak override --filesystem`.
// A library on /mnt/ssd1 needs that mount, not only /mnt, when the bind is not recursive.
func flatpakFilesystemArg(path string) string {
	path = filepath.Clean(path)
	slash := filepath.ToSlash(path)
	for _, spec := range []struct {
		prefix string
		n      int
	}{
		{"/run/media/", 2},
		{"/mnt/", 1},
		{"/media/", 1},
	} {
		rest, ok := strings.CutPrefix(slash, spec.prefix)
		if !ok {
			continue
		}
		parts := strings.Split(rest, "/")
		if len(parts) < spec.n || parts[0] == "" {
			continue
		}
		keep := parts[:spec.n]
		blank := false
		for _, p := range keep {
			if p == "" {
				blank = true
				break
			}
		}
		if blank {
			continue
		}
		return filepath.FromSlash(spec.prefix + strings.Join(keep, "/"))
	}
	if i := strings.Index(strings.ToLower(slash), "/steamapps/"); i > 0 {
		return filepath.FromSlash(slash[:i])
	}
	return filepath.Dir(path)
}

func installPathError(reported string, cause error) error {
	if inFlatpak() {
		return fmt.Errorf("Steam stored the addon at %s, which this Flatpak cannot read (%w)\nAllow that folder, then start GWorX again:\n  flatpak override --user --filesystem=%s eu.p1ka.gworx", reported, cause, flatpakFilesystemArg(reported))
	}
	return fmt.Errorf("Steam stored the addon at %s, but it is not there (%w)", reported, cause)
}
