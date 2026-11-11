package workshop

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const steamcmdCDN = "https://steamcdn-a.akamaihd.net/client/installer/"

// steamcmdArchive is the Valve installer archive for the current OS.
func steamcmdArchive() (url, name string, err error) {
	switch runtime.GOOS {
	case "linux":
		return steamcmdCDN + "steamcmd_linux.tar.gz", "steamcmd_linux.tar.gz", nil
	case "windows":
		return steamcmdCDN + "steamcmd.zip", "steamcmd.zip", nil
	case "darwin":
		return steamcmdCDN + "steamcmd_osx.tar.gz", "steamcmd_osx.tar.gz", nil
	default:
		return "", "", fmt.Errorf("steamcmd is not available for %s", runtime.GOOS)
	}
}

// SteamCMDInDir returns the steamcmd binary inside dir, if present.
func SteamCMDInDir(dir string) string {
	if dir == "" {
		return ""
	}
	for _, name := range []string{"steamcmd.sh", "steamcmd.exe", "steamcmd"} {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// InstallSteamCMD downloads Valve's steamcmd archive into dir, extracts it,
// and runs it once with +quit so it finishes self-updating. wrapper (e.g.
// "steam-run") is prepended when set — required on NixOS for the 32-bit binary.
// It returns the path to the steamcmd launcher (steamcmd.sh / steamcmd.exe).
func InstallSteamCMD(ctx context.Context, dir, wrapper string, log Logger) (string, error) {
	if log == nil {
		log = func(string) {}
	}
	if dir == "" {
		return "", errors.New("steamcmd install folder is empty")
	}
	url, name, err := steamcmdArchive()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	log("Downloading " + url)
	archivePath := filepath.Join(dir, name)
	if err := downloadFile(ctx, url, archivePath); err != nil {
		return "", fmt.Errorf("downloading steamcmd: %w", err)
	}
	defer os.Remove(archivePath)

	log("Extracting into " + dir)
	if strings.HasSuffix(name, ".zip") {
		err = extractZipFile(archivePath, dir)
	} else {
		err = extractTarGzFile(archivePath, dir)
	}
	if err != nil {
		return "", fmt.Errorf("extracting steamcmd: %w", err)
	}

	exe := SteamCMDInDir(dir)
	if exe == "" {
		return "", errors.New("steamcmd archive extracted but no steamcmd binary was found")
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(exe, 0o755)
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			name := d.Name()
			if name == "steamcmd" || strings.HasSuffix(name, ".sh") {
				_ = os.Chmod(path, 0o755)
			}
			return nil
		})
	}

	log("Bootstrapping steamcmd (first run downloads the rest of the client)…")
	if err := bootstrapSteamCMD(ctx, exe, dir, wrapper, log); err != nil {
		// Binary is already in place; missing libs or a cancelled update
		// should not throw away the install path.
		log("Warning: bootstrap did not finish cleanly: " + err.Error())
		if runtime.GOOS == "linux" && strings.TrimSpace(wrapper) == "" {
			log("Hint: on NixOS set Launch wrapper to steam-run (Dedicated servers settings), then Download again")
		}
	}
	log("steamcmd ready: " + exe)
	return exe, nil
}

// wrapCommand prepends wrapper (e.g. "steam-run") to exe and its args.
func wrapCommand(wrapper, exe string, args ...string) (string, []string, error) {
	wrapper = strings.TrimSpace(wrapper)
	if wrapper == "" {
		return exe, args, nil
	}
	parts := strings.Fields(wrapper)
	if len(parts) == 0 {
		return exe, args, nil
	}
	prog, err := exec.LookPath(parts[0])
	if err != nil {
		return "", nil, fmt.Errorf("launch wrapper %q not found", parts[0])
	}
	full := append(append([]string{}, parts[1:]...), exe)
	full = append(full, args...)
	return prog, full, nil
}

func downloadFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s", res.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, res.Body)
	return err
}

func extractTarGzFile(path, dir string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dir, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(hdr.Mode) & 0o777
			if mode == 0 {
				mode = 0o644
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
}

func extractZipFile(path, dir string) error {
	r, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		target, err := safeJoin(dir, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		closeErr := out.Close()
		rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func safeJoin(dir, name string) (string, error) {
	clean := filepath.Clean(name)
	if clean == "." || clean == "" {
		return dir, nil
	}
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("refusing unsafe path %q in archive", name)
	}
	target := filepath.Join(dir, clean)
	rel, err := filepath.Rel(dir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("refusing unsafe path %q in archive", name)
	}
	return target, nil
}

func bootstrapSteamCMD(ctx context.Context, exe, dir, wrapper string, log Logger) error {
	prog, args, err := wrapCommand(wrapper, exe, "+quit")
	if err != nil {
		return err
	}
	if w := strings.TrimSpace(wrapper); w != "" {
		log("$ " + w + " " + exe + " +quit")
	}
	cmd := exec.CommandContext(ctx, prog, args...)
	cmd.Dir = dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	scanLines(stdout, log)
	err = cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// steamcmd often exits non-zero after a successful bootstrap; treat a
	// still-present launcher as success.
	if SteamCMDInDir(dir) == "" {
		if err != nil {
			return err
		}
		return errors.New("steamcmd disappeared after bootstrap")
	}
	return nil
}

func scanLines(r io.Reader, log Logger) {
	buf := make([]byte, 4096)
	var pending string
	for {
		n, err := r.Read(buf)
		if n > 0 {
			pending += string(buf[:n])
			for {
				i := strings.IndexAny(pending, "\r\n")
				if i < 0 {
					break
				}
				line := strings.TrimSpace(pending[:i])
				pending = pending[i+1:]
				if line != "" {
					log(ansiRe.ReplaceAllString(line, ""))
				}
			}
		}
		if err != nil {
			if line := strings.TrimSpace(pending); line != "" {
				log(ansiRe.ReplaceAllString(line, ""))
			}
			return
		}
	}
}
