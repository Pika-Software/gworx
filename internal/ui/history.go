package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Pika-Software/gworx/internal/store"
	"github.com/Pika-Software/gworx/internal/workshop"
)

// historyPage lists addons downloaded with GWorX.
type historyPage struct {
	a *App

	items    []store.Download
	selected int
	list     *widget.List
	details  *widget.Label
	image    *canvas.Image
	imageBox *fyne.Container
	scroll   *container.Scroll
	actions  *fyne.Container
}

func newHistoryPage(a *App) *historyPage {
	h := &historyPage{a: a, selected: -1}

	h.list = widget.NewList(
		func() int { return len(h.items) },
		func() fyne.CanvasObject {
			title := widget.NewLabel("")
			title.TextStyle.Bold = true
			title.Truncation = fyne.TextTruncateEllipsis
			sub := widget.NewLabel("")
			sub.Truncation = fyne.TextTruncateEllipsis
			return container.NewBorder(nil, nil, newThumb(56), nil, container.NewVBox(title, sub))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			it := h.items[i]
			row := o.(*fyne.Container)
			box := row.Objects[0].(*fyne.Container)
			setThumb(row.Objects[1].(*canvas.Image), h.a.previews.workshop(it.ID))
			title := it.Title
			if title == "" {
				title = fmt.Sprintf("Item %d", it.ID)
			}
			box.Objects[0].(*widget.Label).SetText(title)
			box.Objects[1].(*widget.Label).SetText(fmt.Sprintf("%d  ·  %s  ·  %s", it.ID, humanSize(it.Size), it.DownloadedAt.Format("2006-01-02 15:04")))
		},
	)
	h.list.OnSelected = func(i widget.ListItemID) {
		h.selected = i
		h.showDetails()
	}
	h.list.OnUnselected = func(widget.ListItemID) {
		h.selected = -1
		h.showDetails()
	}

	h.details = wrapLabel("")
	h.image = newThumb(192)
	h.image.Hide()
	h.imageBox = container.NewHBox(h.image)
	h.scroll = container.NewVScroll(container.NewVBox(h.imageBox, h.details))
	a.previews.subscribe(func() {
		h.list.Refresh()
		h.showImage()
	})
	h.actions = container.NewHBox()
	h.reload()
	return h
}

func (h *historyPage) current() (store.Download, bool) {
	if h.selected < 0 || h.selected >= len(h.items) {
		return store.Download{}, false
	}
	return h.items[h.selected], true
}

func (h *historyPage) reload() {
	var keep uint64
	if cur, ok := h.current(); ok {
		keep = cur.ID
	}

	h.items = h.a.store.Downloads()
	h.selected = -1
	h.list.UnselectAll()
	h.list.Refresh()

	for i, it := range h.items {
		if it.ID == keep && keep != 0 {
			h.list.Select(i)
			return
		}
	}
	h.showDetails()
}

func exists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

func (h *historyPage) showImage() {
	it, ok := h.current()
	if !ok {
		h.image.Hide()
		h.scroll.Refresh()
		return
	}
	setThumb(h.image, h.a.previews.workshop(it.ID))
	h.image.Show()
	h.scroll.Refresh()
}

func (h *historyPage) showDetails() {
	h.showImage()
	it, ok := h.current()
	if !ok {
		h.details.SetText("Addons downloaded from the Workshop appear here.")
		h.actions.Objects = nil
		h.actions.Refresh()
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\nWorkshop ID: %d\n", it.Title, it.ID)
	if it.Author != "" {
		fmt.Fprintf(&b, "GMA author: %s\n", it.Author)
	}
	fmt.Fprintf(&b, "Downloaded: %s\nSize: %s\n", it.DownloadedAt.Format(time.DateTime), humanSize(it.Size))

	missing := func(p string) string {
		if exists(p) {
			return ""
		}
		return "  (missing)"
	}
	fmt.Fprintf(&b, "GMA: %s%s\n", it.GMAPath, missing(it.GMAPath))
	if it.ExtractedPath != "" {
		fmt.Fprintf(&b, "Extracted: %s%s\n", it.ExtractedPath, missing(it.ExtractedPath))
	}
	h.details.SetText(b.String())

	var acts []fyne.CanvasObject
	if exists(it.GMAPath) {
		acts = append(acts,
			widget.NewButtonWithIcon("Show GMA", theme.FolderIcon(), func() { h.a.openPath(filepath.Dir(it.GMAPath)) }),
			widget.NewButtonWithIcon("Extract…", theme.FolderOpenIcon(), func() {
				dest := it.ExtractedPath
				if dest == "" {
					dest = filepath.Join(h.a.cfg.Uploads(), fmt.Sprintf("%d_%s", it.ID, slug(it.Title)))
				}
				h.a.extract.setGMA(it.GMAPath, dest)
				h.a.showToolsTab(0)
			}),
		)
	}
	if exists(it.ExtractedPath) {
		acts = append(acts, widget.NewButtonWithIcon("Open files", theme.FolderIcon(), func() { h.a.openPath(it.ExtractedPath) }))
	}
	acts = append(acts,
		widget.NewButtonWithIcon("Workshop page", theme.ComputerIcon(), func() { h.a.openURL(workshop.ItemURL(it.ID)) }),
		widget.NewButtonWithIcon("Download again", theme.DownloadIcon(), func() {
			h.a.download.setID(it.ID)
			h.a.showWorkshopTab(0)
		}),
		widget.NewButtonWithIcon("Remove", theme.DeleteIcon(), func() { h.remove(it) }),
	)

	h.actions.Objects = acts
	h.actions.Refresh()
}

func (h *historyPage) remove(it store.Download) {
	delFiles := widget.NewCheck("Also delete the downloaded GMA and extracted files", nil)
	dialog.ShowCustomConfirm("Remove from history", "Remove", "Cancel",
		container.NewVBox(widget.NewLabel(fmt.Sprintf("Remove %q from the history?", it.Title)), delFiles),
		func(ok bool) {
			if !ok {
				return
			}
			if delFiles.Checked {
				if it.GMAPath != "" {
					os.Remove(it.GMAPath)
				}
				if it.ExtractedPath != "" {
					os.RemoveAll(it.ExtractedPath)
				}
			}
			if err := h.a.store.RemoveDownload(it.ID); err != nil {
				h.a.showError(err)
			}
		}, h.a.win)
}

func (h *historyPage) object() fyne.CanvasObject {
	toolbar := container.NewHBox(
		widget.NewButtonWithIcon("Clear history", theme.DeleteIcon(), func() {
			dialog.ShowConfirm("Clear history", "Remove all entries? Files on disk are kept.", func(ok bool) {
				if ok {
					if err := h.a.store.ClearDownloads(); err != nil {
						h.a.showError(err)
					}
				}
			}, h.a.win)
		}),
	)

	right := insetSides(container.NewBorder(nil, container.NewHScroll(h.actions), nil, nil,
		greyPanel(h.scroll)))
	split := container.NewHSplit(h.list, right)
	split.Offset = 0.4

	return container.NewBorder(toolbar, nil, nil, nil, split)
}
