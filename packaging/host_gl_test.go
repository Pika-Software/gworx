package packaging

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHostGL(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("linux appimage wrapper")
	}
	multi := map[string]string{
		"amd64": "x86_64-linux-gnu",
		"arm64": "aarch64-linux-gnu",
	}[runtime.GOARCH]
	if multi == "" {
		t.Skip("unsupported arch")
	}

	t.Run("host drivers", func(t *testing.T) {
		root := t.TempDir()
		libDir := filepath.Join(root, "usr", "lib", multi)
		dri := filepath.Join(libDir, "dri")
		vendor := filepath.Join(root, "usr", "share", "glvnd", "egl_vendor.d")
		if err := os.MkdirAll(dri, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(vendor, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(libDir, "libEGL.so.1"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		env := runHostGL(t, root, map[string]string{
			"LD_LIBRARY_PATH":                "",
			"__EGL_VENDOR_LIBRARY_DIRS":      "",
			"__EGL_VENDOR_LIBRARY_FILENAMES": "",
			"LIBGL_DRIVERS_PATH":             "",
		})
		wantLD := "/nix/libc/lib:" + libDir
		if env["LD_LIBRARY_PATH"] != wantLD {
			t.Fatalf("LD_LIBRARY_PATH = %q, want %q", env["LD_LIBRARY_PATH"], wantLD)
		}
		if env["__EGL_VENDOR_LIBRARY_DIRS"] != vendor {
			t.Fatalf("EGL vendors = %q, want %q", env["__EGL_VENDOR_LIBRARY_DIRS"], vendor)
		}
		if env["LIBGL_DRIVERS_PATH"] != dri {
			t.Fatalf("DRI = %q, want %q", env["LIBGL_DRIVERS_PATH"], dri)
		}
	})

	// Host glibc newer than Nix's: Nix libEGL must stay ahead of the host
	// libEGL, and the vendor JSON's soname (libEGL_nvidia.so.0) must still be
	// reachable. Vendor dirs alone do not put that soname on the loader path.
	t.Run("newer host glibc", func(t *testing.T) {
		root := t.TempDir()
		libDir := filepath.Join(root, "usr", "lib", multi)
		vendor := filepath.Join(root, "usr", "share", "glvnd", "egl_vendor.d")
		if err := os.MkdirAll(libDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(vendor, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(libDir, "libEGL.so.1"), []byte("host"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(libDir, "libEGL_nvidia.so.0"), []byte("nv"), 0o644); err != nil {
			t.Fatal(err)
		}
		nvidiaJSON := filepath.Join(vendor, "10_nvidia.json")
		if err := os.WriteFile(nvidiaJSON, []byte("{\"ICD\":{\"library_path\":\"libEGL_nvidia.so.0\"}}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(vendor, "50_mesa.json"), []byte("{\"ICD\":{\"library_path\":\"libEGL_mesa.so.0\"}}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		const nixLibs = "/nix/libc/lib:/nix/libglvnd/lib"
		env := runHostGL(t, root, map[string]string{
			"LD_LIBRARY_PATH":                "",
			"__EGL_VENDOR_LIBRARY_DIRS":      "",
			"__EGL_VENDOR_LIBRARY_FILENAMES": "",
			"LIBGL_DRIVERS_PATH":             "",
			"GWORX_NIX_LIB_PATH":             nixLibs,
		})
		wantLD := nixLibs + ":" + libDir
		if env["LD_LIBRARY_PATH"] != wantLD {
			t.Fatalf("LD_LIBRARY_PATH = %q, want %q", env["LD_LIBRARY_PATH"], wantLD)
		}
		if !strings.Contains(env["LD_LIBRARY_PATH"], libDir) {
			t.Fatalf("host dir with libEGL_nvidia.so.0 missing from loader path: %q", env["LD_LIBRARY_PATH"])
		}
		if env["__EGL_VENDOR_LIBRARY_DIRS"] != vendor {
			t.Fatalf("EGL vendors = %q, want %q", env["__EGL_VENDOR_LIBRARY_DIRS"], vendor)
		}
		if env["__EGL_VENDOR_LIBRARY_FILENAMES"] != "" {
			t.Fatalf("both vendor JSONs must stay eligible, got FILENAMES=%q", env["__EGL_VENDOR_LIBRARY_FILENAMES"])
		}
	})

	t.Run("nixos unchanged", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "etc", "NIXOS"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		libDir := filepath.Join(root, "usr", "lib", multi)
		if err := os.MkdirAll(libDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(libDir, "libEGL.so.1"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		env := runHostGL(t, root, map[string]string{
			"LD_LIBRARY_PATH":                "keepme",
			"__EGL_VENDOR_LIBRARY_DIRS":      "",
			"__EGL_VENDOR_LIBRARY_FILENAMES": "",
			"LIBGL_DRIVERS_PATH":             "",
		})
		if env["LD_LIBRARY_PATH"] != "keepme" {
			t.Fatalf("LD_LIBRARY_PATH = %q, want keepme", env["LD_LIBRARY_PATH"])
		}
		if env["__EGL_VENDOR_LIBRARY_DIRS"] != "" || env["LIBGL_DRIVERS_PATH"] != "" {
			t.Fatalf("NixOS should not set host GL vars: %#v", env)
		}
	})
}

func runHostGL(t *testing.T, root string, extra map[string]string) map[string]string {
	t.Helper()
	cmd := exec.Command("sh", "host-gl.sh")
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"GWORX_GL_ECHO=1",
		"GWORX_GL_ROOT=" + root,
		"GWORX_NIX_LIB_PATH=/nix/libc/lib",
		"GWORX_REAL=/bin/true",
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("host-gl.sh: %v\n%s", err, out)
	}
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("bad line %q in %s", line, out)
		}
		got[k] = v
	}
	return got
}
