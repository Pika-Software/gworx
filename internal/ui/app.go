// Package ui implements the GWorX Fyne interface.
package ui

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

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

	log *logView

	busy atomic.Bool

	tabs         *container.AppTabs
	workshopTabs *container.AppTabs
	toolsTabs    *container.AppTabs

	download *downloadPage
	upload   *uploadPage
	mine     *minePage
	history  *historyPage
	build    *buildPage
	extract  *extractPage
	settings *settingsPage
}

func New(fa fyne.App, win fyne.Window, cfg *config.Config, st *store.Store) *App {
	a := &App{
		fa:    fa,
		win:   win,
		cfg:   cfg,
		store: st,
		log:   newLogView(),
	}
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
	a.svc.Close()
}

func (a *App) Content() fyne.CanvasObject {
	a.download = newDownloadPage(a)
	a.upload = newUploadPage(a)
	a.mine = newMinePage(a)
	a.history = newHistoryPage(a)
	a.build = newBuildPage(a)
	a.extract = newExtractPage(a)
	a.settings = newSettingsPage(a)

	a.workshopTabs = container.NewAppTabs(
		container.NewTabItemWithIcon("Download", theme.DownloadIcon(), a.download.object()),
		container.NewTabItemWithIcon("Upload", theme.UploadIcon(), a.upload.object()),
		container.NewTabItemWithIcon("My Addons", theme.AccountIcon(), a.mine.object()),
		container.NewTabItemWithIcon("History", theme.HistoryIcon(), a.history.object()),
	)
	a.workshopTabs.SetTabLocation(container.TabLocationLeading)
	a.workshopTabs.OnSelected = func(*container.TabItem) { a.upload.refreshBackendLabel() }

	a.toolsTabs = container.NewAppTabs(
		container.NewTabItemWithIcon("Build", theme.DocumentCreateIcon(), a.build.object()),
		container.NewTabItemWithIcon("Extract", theme.FolderOpenIcon(), a.extract.object()),
	)
	a.toolsTabs.SetTabLocation(container.TabLocationLeading)

	a.tabs = container.NewAppTabs(
		container.NewTabItem("Steam Workshop", a.workshopTabs),
		container.NewTabItem("GMA Tools", a.toolsTabs),
		container.NewTabItem("Dedicated Server", container.NewCenter(widget.NewLabel("Coming soon"))),
		container.NewTabItem("Log", a.log.object(a)),
		container.NewTabItem("Settings", a.settings.object()),
	)

	return a.tabs
}

// --- navigation -------------------------------------------------------------

func (a *App) showWorkshopTab(i int) {
	a.tabs.SelectIndex(0)
	a.workshopTabs.SelectIndex(i)
}

func (a *App) showToolsTab(i int) {
	a.tabs.SelectIndex(1)
	a.toolsTabs.SelectIndex(i)
}

// --- backend ----------------------------------------------------------------

func (a *App) backend() workshop.Backend { return a.svc.Backend() }

func (a *App) defaultAuthor() string { return a.svc.DefaultAuthor() }

// --- credential prompts (workshop.Prompter) ---------------------------------

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

// --- tasks ------------------------------------------------------------------

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

	status := widget.NewLabel("Working...")
	status.Wrapping = fyne.TextWrapWord
	bar := widget.NewProgressBar()
	bar.Hide()
	inf := widget.NewProgressBarInfinite()

	lv := newLogView()
	logObj := lv.list
	scroll := container.NewStack(logObj)

	content := container.NewBorder(container.NewVBox(status, inf, bar), nil, nil, nil, scroll)

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

// --- file pickers -----------------------------------------------------------

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

// pickFolder opens a folder chooser starting at start and calls done with the selected path.
func (a *App) pickFolder(start string, done func(string)) {
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

// pickFile opens a file chooser filtered by extensions.
func (a *App) pickFile(start string, exts []string, done func(string)) {
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

// --- misc helpers -----------------------------------------------------------

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

func slug(s string) string { return core.Slug(s) }

func humanSize(n int64) string { return core.HumanSize(n) }

func wrapLabel(s string) *widget.Label {
	l := widget.NewLabel(s)
	l.Wrapping = fyne.TextWrapWord
	return l
}
