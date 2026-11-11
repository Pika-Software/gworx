package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
	"github.com/ncruces/zenity"
)

// errNoNativeDialog means no system file chooser is available; callers fall
// back to Fyne's built-in dialog.
var errNoNativeDialog = errors.New("no native file dialog available")

// fileRequest describes a native open dialog.
type fileRequest struct {
	title  string
	start  string   // initial folder (or a file inside it)
	folder bool     // pick a folder instead of a file
	exts   []string // allowed extensions, e.g. ".gma"
	parent string   // window handle for the portal ("x11:<id>"), may be empty
}

// startFolder returns an existing folder to open the dialog in.
func (r fileRequest) startFolder() string {
	dir := r.start
	for dir != "" {
		if st, err := os.Stat(dir); err == nil {
			if st.IsDir() {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	home, _ := os.UserHomeDir()
	return home
}

// windowHandle returns the X11 window id for portal dialogs ("" on Wayland).
// It must be called on the UI thread.
func (a *App) windowHandle() string {
	handle := ""
	if nw, ok := a.win.(driver.NativeWindow); ok {
		nw.RunNative(func(ctx any) {
			if x11, ok := ctx.(driver.X11WindowContext); ok && x11.WindowHandle != 0 {
				handle = "x11:" + strconv.FormatUint(uint64(x11.WindowHandle), 16)
			}
		})
	}
	return handle
}

// zenityPick uses the zenity package: native dialogs on Windows and macOS,
// zenity/kdialog/qarma on other Unixes.
func zenityPick(r fileRequest) (string, error) {
	opts := []zenity.Option{zenity.Title(r.title)}
	if dir := r.startFolder(); dir != "" {
		opts = append(opts, zenity.Filename(dir+string(filepath.Separator)))
	}
	if r.folder {
		opts = append(opts, zenity.Directory())
	} else if len(r.exts) > 0 {
		patterns := make([]string, len(r.exts))
		for i, e := range r.exts {
			patterns[i] = "*" + e
		}
		opts = append(opts, zenity.FileFilters{{Name: strings.Join(patterns, ", "), Patterns: patterns, CaseFold: true}})
	}

	p, err := zenity.SelectFile(opts...)
	if errors.Is(err, zenity.ErrCanceled) {
		return "", nil
	}
	return p, err
}

// pickNative shows the system dialog in the background and calls done with
// the chosen path on the UI thread. fallback runs if no native dialog exists.
func (a *App) pickNative(r fileRequest, done func(string), fallback func()) {
	r.parent = a.windowHandle()
	go func() {
		p, err := nativePick(r)
		fyne.Do(func() {
			switch {
			case errors.Is(err, errNoNativeDialog):
				fallback()
			case err != nil:
				a.showError(err)
			case p != "":
				done(p)
			}
		})
	}()
}
