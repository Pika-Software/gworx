package ui

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	portalBus      = "org.freedesktop.portal.Desktop"
	portalPath     = dbus.ObjectPath("/org/freedesktop/portal/desktop")
	portalOpenFile = "org.freedesktop.portal.FileChooser.OpenFile"
	portalResponse = "org.freedesktop.portal.Request.Response"
)

var portalToken atomic.Uint64

// portalFilter is the D-Bus form of a file filter: (sa(us)).
type portalFilter struct {
	Name  string
	Rules []portalRule
}

type portalRule struct {
	Type    uint32 // 0 = glob pattern
	Pattern string
}

// errHostToolMissing means this helper is not installed. Another one may be.
var errHostToolMissing = errors.New("file dialog helper not installed")

// nativePick uses the XDG desktop portal (the desktop's own file chooser on
// GNOME, KDE, ...). An AppImage runs in a private user namespace, and current
// portals then refuse the request ("Unable to open /proc/<pid>/root") without
// showing a window. kdialog — what CachyOS/KDE ships — or zenity is the
// system chooser in that case. They are host programs, so they must not
// inherit the AppImage's LD_LIBRARY_PATH: Nix's libc makes them exit before
// the window is mapped.
func nativePick(r fileRequest) (string, error) {
	p, err := portalPick(r)
	if !errors.Is(err, errNoNativeDialog) {
		return p, err
	}
	return hostToolPick(r)
}

func hostToolPick(r fileRequest) (string, error) {
	tools := []struct {
		name string
		args []string
	}{
		{"kdialog", kdialogArgs(r)},
		{"zenity", zenityArgs(r)},
		{"qarma", zenityArgs(r)},
	}
	for _, tool := range tools {
		p, err := runHostCommand(tool.name, tool.args...)
		if err != nil {
			continue
		}
		return cleanDialogPath(p), nil
	}
	return "", errNoNativeDialog
}

func kdialogArgs(r fileRequest) []string {
	args := []string{"--title", r.title, "--getopenfilename", r.startFolder()}
	if r.folder {
		args = []string{"--title", r.title, "--getexistingdirectory", r.startFolder()}
	} else if f := toolFilter(r.exts); f != "" {
		args = append(args, f)
	}
	return args
}

func zenityArgs(r fileRequest) []string {
	args := []string{"--file-selection", "--title=" + r.title}
	if dir := r.startFolder(); dir != "" {
		args = append(args, "--filename="+dir+string(os.PathSeparator))
	}
	if r.folder {
		args = append(args, "--directory")
	} else if f := toolFilter(r.exts); f != "" {
		args = append(args, "--file-filter="+f)
	}
	return args
}

func toolFilter(exts []string) string {
	pats := extPatterns(exts)
	if len(pats) == 0 {
		return ""
	}
	shown := strings.Join(pats, " ")
	return shown + "|" + shown
}

func extPatterns(exts []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, e := range exts {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") && !strings.HasPrefix(e, "*") {
			e = "." + e
		}
		if strings.HasPrefix(e, "*") {
			add(strings.ToLower(e))
			add(strings.ToUpper(e))
			continue
		}
		add("*" + strings.ToLower(e))
		add("*" + strings.ToUpper(e))
	}
	return out
}

func runHostCommand(name string, args ...string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", errHostToolMissing
	}
	cmd := exec.Command(path, args...)
	cmd.Env = hostEnv()
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", nil
		}
		return "", err
	}
	return string(out), nil
}

func hostEnv() []string {
	return filterHostEnv(os.Environ())
}

func filterHostEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		key, _, _ := strings.Cut(e, "=")
		switch key {
		case "LD_LIBRARY_PATH", "LD_PRELOAD", "LD_AUDIT",
			"LIBGL_DRIVERS_PATH",
			"__EGL_VENDOR_LIBRARY_DIRS", "__EGL_VENDOR_LIBRARY_FILENAMES":
			continue
		}
		out = append(out, e)
	}
	return out
}

func cleanDialogPath(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "file://") {
		if u, err := url.Parse(p); err == nil && u.Path != "" {
			return u.Path
		}
	}
	return p
}

// portalPick talks to org.freedesktop.portal.FileChooser directly. It uses a
// private bus connection and subscribes to the Response of its own request
// before opening the dialog, as the portal documentation requires, so other
// signals on the bus (Fyne listens to the settings portal) can't be mistaken
// for the answer.
func portalPick(r fileRequest) (string, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return "", errNoNativeDialog
	}
	defer conn.Close()

	token := "gworx" + strconv.FormatUint(portalToken.Add(1), 10) + "_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	sender := strings.ReplaceAll(strings.TrimPrefix(conn.Names()[0], ":"), ".", "_")
	expected := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + sender + "/" + token)

	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	match := []dbus.MatchOption{
		dbus.WithMatchInterface("org.freedesktop.portal.Request"),
		dbus.WithMatchMember("Response"),
		dbus.WithMatchObjectPath(expected),
	}
	if err := conn.AddMatchSignal(match...); err != nil {
		return "", errNoNativeDialog
	}

	opts := map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant(token),
		"modal":        dbus.MakeVariant(true),
		"directory":    dbus.MakeVariant(r.folder),
	}
	if dir := r.startFolder(); dir != "" {
		opts["current_folder"] = dbus.MakeVariant(append([]byte(dir), 0))
	}
	if !r.folder && len(r.exts) > 0 {
		f := portalFilter{}
		var names []string
		for _, e := range r.exts {
			names = append(names, "*"+e)
			f.Rules = append(f.Rules,
				portalRule{Pattern: "*" + strings.ToLower(e)},
				portalRule{Pattern: "*" + strings.ToUpper(e)})
		}
		f.Name = strings.Join(names, ", ")
		opts["filters"] = dbus.MakeVariant([]portalFilter{f})
		opts["current_filter"] = dbus.MakeVariant(f)
	}

	var handle dbus.ObjectPath
	call := conn.Object(portalBus, portalPath).Call(portalOpenFile, 0, r.parent, r.title, opts)
	if call.Err != nil {
		return "", errNoNativeDialog // no portal / no FileChooser implementation
	}
	if err := call.Store(&handle); err != nil {
		return "", errNoNativeDialog
	}
	if handle != expected {
		// very old portals ignore handle_token; follow the returned path
		conn.AddMatchSignal(
			dbus.WithMatchInterface("org.freedesktop.portal.Request"),
			dbus.WithMatchMember("Response"),
			dbus.WithMatchObjectPath(handle),
		)
	}

	// From here on the system dialog is open: never fall back to a second one.
	for sig := range signals {
		if sig.Path != handle || sig.Name != portalResponse || len(sig.Body) != 2 {
			continue
		}
		status, _ := sig.Body[0].(uint32)
		results, _ := sig.Body[1].(map[string]dbus.Variant)
		switch status {
		case 0: // success
		case 1: // cancelled
			return "", nil
		default:
			return "", errors.New("the file dialog was closed")
		}

		var uris []string
		if v, ok := results["uris"]; ok {
			uris, _ = v.Value().([]string)
		}
		if len(uris) == 0 {
			return "", nil
		}
		u, err := url.Parse(uris[0])
		if err != nil || u.Scheme != "file" {
			return "", fmt.Errorf("unsupported location %q", uris[0])
		}
		return u.Path, nil
	}
	return "", errors.New("lost the connection to the file dialog")
}
