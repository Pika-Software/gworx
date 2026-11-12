// Package ui implements the GWorX Fyne interface.
package ui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Pika-Software/gworx/internal/config"
	"github.com/Pika-Software/gworx/internal/core"
	"github.com/Pika-Software/gworx/internal/store"
	"github.com/Pika-Software/gworx/internal/workshop"
)

type App struct {
	fa    fyne.App
	win   fyne.Window
	svc   *core.Service
	cfg   *config.Config
	store *store.Store

	log      *logView
	previews *previewCache

	busy atomic.Bool
	// cancelTask cancels the operation started by runTask (nil when idle).
	taskMu     sync.Mutex
	cancelTask context.CancelFunc

	tabs         *container.AppTabs
	workshopTabs *sideTabs
	toolsTabs    *sideTabs

	download *downloadPage
	upload   *uploadPage
	mine     *minePage
	history  *historyPage
	build    *buildPage
	extract  *extractPage
	settings *settingsPage
	servers  *serverPage
}

func New(fa fyne.App, win fyne.Window, cfg *config.Config, st *store.Store) *App {
	a := &App{
		fa:    fa,
		win:   win,
		cfg:   cfg,
		store: st,
		log:   newLogView(),
	}
	a.previews = newPreviewCache()
	a.svc = core.New(cfg, st, a)

	st.OnChange = func() {
		fyne.Do(func() {
			if a.mine != nil {
				a.mine.reload()
			}
			if a.history != nil {
				a.history.reload()
			}
		})
	}

	return a
}

// Shutdown releases the Steam API if it was used.
func (a *App) Shutdown() {
	a.svc.Servers.StopAll()
	a.svc.Close()
}

// RequestClose closes the window, asking first when an operation or
// dedicated servers are still running.
func (a *App) RequestClose() {
	var running []string
	if a.busy.Load() {
		running = append(running, "an operation is still running (it will be cancelled)")
	}
	if n := a.svc.Servers.Running(); n > 0 {
		running = append(running, fmt.Sprintf("%d dedicated server(s) still running (they will be stopped)", n))
	}
	if len(running) == 0 {
		a.quit()
		return
	}
	dialog.ShowConfirm("Quit GWorX", "Quit anyway?\n\n- "+strings.Join(running, "\n- "), func(ok bool) {
		if ok {
			a.quit()
		}
	}, a.win)
}

// quit cancels the running task, stops servers and closes the window without
// blocking the UI thread.
func (a *App) quit() {
	a.taskMu.Lock()
	if a.cancelTask != nil {
		a.cancelTask()
	}
	a.taskMu.Unlock()

	wait := dialog.NewCustomWithoutButtons("Quitting", widget.NewProgressBarInfinite(), a.win)
	wait.Show()
	go func() {
		a.Shutdown()
		fyne.Do(func() {
			wait.Hide()
			a.win.Close()
		})
	}()
}

func (a *App) Content() fyne.CanvasObject {
	a.download = newDownloadPage(a)
	a.upload = newUploadPage(a)
	a.mine = newMinePage(a)
	a.history = newHistoryPage(a)
	a.build = newBuildPage(a)
	a.extract = newExtractPage(a)
	a.settings = newSettingsPage(a)
	a.servers = newServerPage(a)

	a.workshopTabs = newSideTabs(
		sideTab{"Download", theme.DownloadIcon(), inset(a.download.object())},
		sideTab{"Upload", theme.UploadIcon(), inset(a.upload.object())},
		sideTab{"My Addons", theme.AccountIcon(), inset(a.mine.object())},
		sideTab{"History", theme.HistoryIcon(), inset(a.history.object())},
	)
	a.workshopTabs.OnSelected = func(int) { a.upload.refreshBackendLabel() }

	a.toolsTabs = newSideTabs(
		sideTab{"Extract", theme.FolderOpenIcon(), inset(a.extract.object())},
		sideTab{"Build", theme.DocumentCreateIcon(), inset(a.build.object())},
	)

	// Match rail width across Workshop and GMA Tools (longest label wins).
	noteSideTabWidth(a.workshopTabs.buttonMinWidth())
	noteSideTabWidth(a.toolsTabs.buttonMinWidth())
	a.workshopTabs.Refresh()
	a.toolsTabs.Refresh()

	a.tabs = container.NewAppTabs(
		container.NewTabItem("Steam Workshop", inset(a.workshopTabs)),
		container.NewTabItem("GMA Tools", inset(a.toolsTabs)),
		container.NewTabItem("Dedicated Server", inset(a.servers.object())),
		container.NewTabItem("Log", inset(a.log.object(a))),
		container.NewTabItem("Settings", inset(a.settings.object())),
	)

	a.win.SetOnDropped(a.handleDrop)

	return a.tabs
}

func (a *App) showWorkshopTab(i int) {
	a.tabs.SelectIndex(0)
	a.workshopTabs.SelectIndex(i)
}

func (a *App) showToolsTab(i int) {
	a.tabs.SelectIndex(1)
	a.toolsTabs.SelectIndex(i)
}

func (a *App) backend() workshop.Backend { return a.svc.Backend() }

func (a *App) defaultAuthor() string { return a.svc.DefaultAuthor() }

func (a *App) askSecret(title, label, hint string, password bool) (string, bool) {
	type answer struct {
		v  string
		ok bool
	}
	ch := make(chan answer, 1)

	fyne.Do(func() {
		var entry *widget.Entry
		if password {
			entry = widget.NewPasswordEntry()
		} else {
			entry = widget.NewEntry()
		}
		item := widget.NewFormItem(label, entry)
		item.HintText = hint

		d := dialog.NewForm(title, "OK", "Cancel", []*widget.FormItem{item}, func(ok bool) {
			ch <- answer{entry.Text, ok}
		}, a.win)
		d.Resize(fyne.NewSize(420, 0))
		d.Show()
		a.win.Canvas().Focus(entry)
	})

	r := <-ch
	return r.v, r.ok
}

func (a *App) Password(user string) (string, bool) {
	return a.askSecret("Steam login", "Password", "steamcmd asks for the password of "+user+". It is sent to steamcmd only and never saved by GWorX.", true)
}

func (a *App) GuardCode(user string, hint string) (string, bool) {
	return a.askSecret("Steam Guard", "Code", hint, false)
}

type task struct {
	ctx      context.Context
	log      func(string)
	progress func(float64)
}

func (t *task) reporter() core.Reporter {
	return core.Reporter{Log: t.log, Progress: t.progress}
}

// runTask executes fn in the background showing a progress dialog with a live log.
// onSuccess runs on the UI thread when fn returns nil.
func (a *App) runTask(title string, fn func(t *task) error, onSuccess func()) {
	if !a.busy.CompareAndSwap(false, true) {
		dialog.ShowInformation("Busy", "Another operation is still running.", a.win)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.taskMu.Lock()
	a.cancelTask = cancel
	a.taskMu.Unlock()

	status := widget.NewLabel("Working...")
	status.Wrapping = fyne.TextWrapWord
	bar := widget.NewProgressBar()
	bar.TextFormatter = func() string { return "" }
	bar.Hide()
	inf := widget.NewProgressBarInfinite()

	lv := newLogView()

	// Stack the two bars so show/hide doesn't jump the dialog layout.
	content := container.NewBorder(container.NewVBox(status, container.NewStack(inf, bar)), nil, nil, nil, lv.list)

	d := dialog.NewCustomWithoutButtons(title, content, a.win)
	cancelBtn := widget.NewButtonWithIcon("Cancel", theme.CancelIcon(), func() {
		cancel()
		status.SetText("Cancelling...")
	})
	closeBtn := widget.NewButton("Close", func() { d.Hide() })
	closeBtn.Hide()
	d.SetButtons([]fyne.CanvasObject{cancelBtn, closeBtn})
	d.Resize(fyne.NewSize(720, 460))
	d.Show()

	var mu sync.Mutex
	lastFrac := -2.0

	t := &task{
		ctx: ctx,
		log: func(s string) {
			for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
				a.log.add(line)
				lv.add(line)
			}
		},
		progress: func(f float64) {
			mu.Lock()
			if f == lastFrac {
				mu.Unlock()
				return
			}
			lastFrac = f
			mu.Unlock()

			fyne.Do(func() {
				if f < 0 {
					bar.Hide()
					inf.Show()
					inf.Start()
				} else {
					inf.Stop()
					inf.Hide()
					bar.Show()
					bar.SetValue(min(f, 1))
				}
			})
		},
	}

	t.log("== " + title)

	go func() {
		err := fn(t)
		cancel()
		a.taskMu.Lock()
		a.cancelTask = nil
		a.taskMu.Unlock()
		a.busy.Store(false)

		if err != nil {
			t.log("ERROR: " + err.Error())
		} else {
			t.log("Done.")
		}

		fyne.Do(func() {
			inf.Stop()
			cancelBtn.Hide()
			closeBtn.Show()

			if err != nil {
				if errors.Is(err, context.Canceled) {
					status.SetText("Cancelled.")
				} else {
					status.SetText("Failed: " + err.Error())
					status.Importance = widget.DangerImportance
					status.Refresh()
				}
				return
			}

			d.Hide()
			if onSuccess != nil {
				onSuccess()
			}
		})
	}()
}

func listable(dir string) fyne.ListableURI {
	for dir != "" {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			if l, err := storage.ListerForURI(storage.NewFileURI(dir)); err == nil {
				return l
			}
			return nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil
}

func (a *App) dialogSize() fyne.Size {
	s := a.win.Canvas().Size()
	return fyne.NewSize(max(s.Width*0.9, 600), max(s.Height*0.9, 450))
}

// pickFolder opens the system folder chooser (Fyne's as a fallback) starting
// at start and calls done with the selected path.
func (a *App) pickFolder(start string, done func(string)) {
	a.pickNative(fileRequest{title: "Select folder", start: start, folder: true}, done,
		func() { a.fynePickFolder(start, done) })
}

// pickFile opens the system file chooser (Fyne's as a fallback) filtered by extensions.
func (a *App) pickFile(start string, exts []string, done func(string)) {
	a.pickNative(fileRequest{title: "Select file", start: start, exts: exts}, done,
		func() { a.fynePickFile(start, exts, done) })
}

func (a *App) fynePickFolder(start string, done func(string)) {
	d := dialog.NewFolderOpen(func(u fyne.ListableURI, err error) {
		if err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		if u != nil {
			done(u.Path())
		}
	}, a.win)
	if l := listable(start); l != nil {
		d.SetLocation(l)
	}
	d.Resize(a.dialogSize())
	d.Show()
}

func (a *App) fynePickFile(start string, exts []string, done func(string)) {
	d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		if r != nil {
			p := r.URI().Path()
			r.Close()
			done(p)
		}
	}, a.win)
	if len(exts) > 0 {
		d.SetFilter(storage.NewExtensionFileFilter(exts))
	}
	if start != "" {
		if st, err := os.Stat(start); err == nil && !st.IsDir() {
			start = filepath.Dir(start)
		}
		if l := listable(start); l != nil {
			d.SetLocation(l)
		}
	}
	d.Resize(a.dialogSize())
	d.Show()
}

// pathEntry returns an entry with a browse button.
func (a *App) pathEntry(e *widget.Entry, folder bool, exts []string, after func(string)) fyne.CanvasObject {
	btn := widget.NewButtonWithIcon("", theme.FolderOpenIcon(), func() {
		cb := func(p string) {
			e.SetText(p)
			if after != nil {
				after(p)
			}
		}
		if folder {
			a.pickFolder(e.Text, cb)
		} else {
			a.pickFile(e.Text, exts, cb)
		}
	})
	return container.NewBorder(nil, nil, nil, btn, e)
}

func (a *App) openPath(p string) {
	if p == "" {
		return
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		dialog.ShowError(err, a.win)
		return
	}
	if _, err := os.Stat(abs); err != nil {
		dialog.ShowError(err, a.win)
		return
	}
	u := &url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	if runtime.GOOS == "windows" {
		u.Path = "/" + u.Path
	}
	if err := a.fa.OpenURL(u); err != nil {
		dialog.ShowError(err, a.win)
	}
}

func (a *App) openURL(raw string) {
	u, err := url.Parse(raw)
	if err == nil {
		err = a.fa.OpenURL(u)
	}
	if err != nil {
		dialog.ShowError(err, a.win)
	}
}

func (a *App) showError(err error) {
	dialog.ShowError(err, a.win)
}

// truncate shortens s to at most n runes, adding an ellipsis when cut.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

func wrapLabel(s string) *widget.Label {
	l := widget.NewLabel(s)
	l.Wrapping = fyne.TextWrapWord
	l.Selectable = true
	return l
}
