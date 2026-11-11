package ui

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Pika-Software/gworx/internal/gmaops"
)

type extractPage struct {
	a *App

	gmaPath *widget.Entry
	dest    *widget.Entry
	verify  *widget.Check
	info    *widget.Label
	files   []gmaops.FileInfo
	list    *widget.List
}

func newExtractPage(a *App) *extractPage {
	e := &extractPage{a: a}

	e.gmaPath = widget.NewEntry()
	e.gmaPath.SetPlaceHolder("Path to a .gma file")
	e.dest = widget.NewEntry()
	e.dest.SetPlaceHolder("Folder to extract into")
	e.verify = widget.NewCheck("Verify file checksums", nil)
	e.verify.SetChecked(true)

	e.info = wrapLabel("No file selected.")
	e.list = widget.NewList(
		func() int { return len(e.files) },
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.TextStyle.Monospace = true
			l.Truncation = fyne.TextTruncateEllipsis
			return l
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			f := e.files[i]
			o.(*widget.Label).SetText(fmt.Sprintf("%10s  %s", humanSize(f.Size), f.Path))
		},
	)
	return e
}

// setGMA selects a file, inspects it and proposes a destination.
func (e *extractPage) setGMA(p string, dest string) {
	e.gmaPath.SetText(p)
	if dest == "" {
		base := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		dest = filepath.Join(e.a.cfg.Uploads(), base)
	}
	e.dest.SetText(dest)
	e.inspect()
}

func (e *extractPage) inspect() {
	p := strings.TrimSpace(e.gmaPath.Text)
	if p == "" {
		return
	}
	inf, err := gmaops.Inspect(p)
	if err != nil {
		e.files = nil
		e.list.Refresh()
		e.info.SetText("Can't read " + p + ": " + err.Error())
		return
	}

	var total int64
	for _, f := range inf.Files {
		total += f.Size
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Title: %s\nAuthor: %s\nAddon version: %d\n", inf.Title, inf.Author, inf.Version)
	if inf.Timestamp > 0 {
		fmt.Fprintf(&b, "Created: %s\n", time.Unix(inf.Timestamp, 0).Format(time.DateTime))
	}
	if len(inf.RequiredContent) > 0 {
		fmt.Fprintf(&b, "Required content: %s\n", strings.Join(inf.RequiredContent, ", "))
	}
	fmt.Fprintf(&b, "Files: %d (%s)", len(inf.Files), humanSize(total))
	if inf.Legacy {
		b.WriteString("\nLegacy LZMA-compressed addon")
	}

	e.info.SetText(b.String())
	e.files = inf.Files
	e.list.Refresh()
}

func (e *extractPage) run() {
	src := strings.TrimSpace(e.gmaPath.Text)
	dest := strings.TrimSpace(e.dest.Text)
	if src == "" || dest == "" {
		e.a.showError(errors.New("choose a GMA file and a destination folder"))
		return
	}
	verify := e.verify.Checked

	var n int
	e.a.runTask("Extract GMA", func(t *task) error {
		var err error
		n, err = e.a.svc.Extract(src, dest, verify, t.reporter())
		return err
	}, func() {
		dialog.NewCustomConfirm("Extracted", "Open folder", "Close",
			wrapLabel(fmt.Sprintf("%d files extracted to %s", n, dest)),
			func(open bool) {
				if open {
					e.a.openPath(dest)
				}
			}, e.a.win).Show()
	})
}

func (e *extractPage) object() fyne.CanvasObject {
	form := widget.NewForm(
		widget.NewFormItem("GMA file", e.a.pathEntry(e.gmaPath, false, []string{".gma", ".bin"}, func(p string) { e.setGMA(p, "") })),
		widget.NewFormItem("Destination", e.a.pathEntry(e.dest, true, nil, nil)),
		widget.NewFormItem("", e.verify),
	)

	extractBtn := widget.NewButtonWithIcon("Extract", theme.DownloadIcon(), e.run)
	extractBtn.Importance = widget.HighImportance

	buttons := container.NewHBox(
		extractBtn,
		widget.NewButtonWithIcon("Reload info", theme.ViewRefreshIcon(), e.inspect),
		widget.NewButtonWithIcon("Open destination", theme.FolderIcon(), func() { e.a.openPath(e.dest.Text) }),
	)

	top := container.NewVBox(form, buttons, widget.NewSeparator(), e.info)
	return container.NewBorder(top, nil, nil, nil, e.list)
}
