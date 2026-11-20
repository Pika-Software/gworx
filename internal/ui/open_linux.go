//go:build linux

package ui

import (
	"net/url"
	"os"
	"os/exec"
)

// openExternal replaces Fyne's OpenURL, which starts xdg-open with our whole
// environment. xdg-open is a host script, and under the AppImage the Nix libc
// on LD_LIBRARY_PATH crashes the host shell before it runs ("stack smashing
// detected" in __libc_early_init).
func (a *App) openExternal(u *url.URL) error {
	path, err := exec.LookPath("xdg-open")
	if err != nil {
		return err
	}
	cmd := exec.Command(path, u.String())
	cmd.Env = hostEnv()
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
