package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Pika-Software/gworx/internal/core"
	"github.com/Pika-Software/gworx/internal/store"
	"github.com/Pika-Software/gworx/internal/workshop"
)

type downloadPage struct {
	a *App

	input   *widget.Entry
	extract *widget.Check
	preview *canvas.Image
	title   *widget.Label
	meta    *widget.Label
	desc    *widget.Label

	infoSeq int
	shownID uint64
}

func newDownloadPage(a *App) *downloadPage {
	d := &downloadPage{a: a}

	d.input = widget.NewEntry()
	d.input.SetPlaceHolder("https://steamcommunity.com/sharedfiles/filedetails/?id=...  or  123456789")
	d.input.OnSubmitted = func(string) { d.fetchInfo() }

	d.extract = widget.NewCheck("Extract files to the uploads folder", nil)
	d.extract.SetChecked(a.cfg.AutoExtract)

	d.preview = newThumb(160)
	d.preview.Hide()
	a.previews.subscribe(func() {
		if d.shownID != 0 {
			setThumb(d.preview, a.previews.workshop(d.shownID))
		}
	})

	d.title = widget.NewLabel("")
	d.title.TextStyle.Bold = true
	d.title.Wrapping = fyne.TextWrapWord
	d.meta = wrapLabel("Paste a Workshop link or ID and press Enter to see the addon.")
	d.desc = wrapLabel("")

	return d
}

func (d *downloadPage) setID(id uint64) {
	d.input.SetText(strconv.FormatUint(id, 10))
	d.fetchInfo()
}

// fetchInfo shows public item details from the Steam Web API.
func (d *downloadPage) fetchInfo() {
	id, err := workshop.ParseID(d.input.Text)
	if err != nil {
		d.a.showError(err)
		return
	}

	d.infoSeq++
	seq := d.infoSeq
	d.title.SetText(fmt.Sprintf("Loading %d...", id))
	d.meta.SetText("")
	d.desc.SetText("")
	d.shownID = id
	setThumb(d.preview, d.a.previews.workshop(id))
	d.preview.Show()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		items, err := workshop.GetDetails(ctx, id)

		fyne.Do(func() {
			if seq != d.infoSeq {
				return
			}
			if err != nil {
				d.title.SetText(fmt.Sprintf("Item %d", id))
				d.meta.SetText("Could not load details: " + err.Error())
				return
			}
			it := items[0]
			d.title.SetText(it.Title)

			var b strings.Builder
			fmt.Fprintf(&b, "ID: %d", it.ID)
			if it.FileSize > 0 {
				fmt.Fprintf(&b, "   Size: %s", humanSize(it.FileSize))
			}
			if it.Updated > 0 {
				fmt.Fprintf(&b, "   Updated: %s", time.Unix(it.Updated, 0).Format(time.DateOnly))
			}
			if len(it.Tags) > 0 {
				fmt.Fprintf(&b, "\nTags: %s", strings.Join(it.Tags, ", "))
			}
			d.meta.SetText(b.String())

			desc := it.Description
			if len(desc) > 1500 {
				desc = desc[:1500] + "…"
			}
			d.desc.SetText(desc)

		})
	}()
}

func (d *downloadPage) run() {
	id, err := workshop.ParseID(d.input.Text)
	if err != nil {
		d.a.showError(err)
		return
	}
	extract := d.extract.Checked
	var rec store.Download

	d.a.runTask(fmt.Sprintf("Download %d", id), func(t *task) error {
		var err error
		rec, err = d.a.svc.Download(t.ctx, id, core.DownloadOptions{Extract: extract}, t.reporter())
		return err
	}, func() {
		target := rec.ExtractedPath
		if target == "" {
			target = filepath.Dir(rec.GMAPath)
		}
		msg := fmt.Sprintf("%s\n\nGMA: %s", rec.Title, rec.GMAPath)
		if rec.ExtractedPath != "" {
			msg += "\nFiles: " + rec.ExtractedPath
		}
		dialog.NewCustomConfirm("Download complete", "Open folder", "Close", wrapLabel(msg), func(open bool) {
			if open {
				d.a.openPath(target)
			}
		}, d.a.win).Show()
	})
}

func (d *downloadPage) object() fyne.CanvasObject {
	infoBtn := widget.NewButtonWithIcon("", theme.SearchIcon(), d.fetchInfo)
	dlBtn := widget.NewButtonWithIcon("Download", theme.DownloadIcon(), d.run)
	dlBtn.Importance = widget.HighImportance

	top := container.NewVBox(
		widget.NewLabel("Workshop link or ID"),
		container.NewBorder(nil, nil, nil, infoBtn, d.input),
		container.NewHBox(d.extract),
		container.NewHBox(dlBtn,
			widget.NewButtonWithIcon("Open downloads", theme.FolderIcon(), func() {
				os.MkdirAll(d.a.cfg.Downloads(), 0o755)
				d.a.openPath(d.a.cfg.Downloads())
			}),
			widget.NewButtonWithIcon("Open uploads", theme.FolderIcon(), func() {
				os.MkdirAll(d.a.cfg.Uploads(), 0o755)
				d.a.openPath(d.a.cfg.Uploads())
			}),
		),
		widget.NewSeparator(),
	)

	card := container.NewBorder(nil, nil, container.NewVBox(d.preview), nil,
		container.NewVBox(d.title, d.meta))

	return container.NewBorder(top, nil, nil, nil, container.NewVScroll(container.NewVBox(card, d.desc)))
}
