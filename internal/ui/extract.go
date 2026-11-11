package ui

import (
	"errors"
	"fmt"
	"image/color"
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

	"github.com/Pika-Software/gworx/internal/core"
	"github.com/Pika-Software/gworx/internal/gmaops"
)

var gmaExts = []string{".gma", ".bin"}

func isGMAPath(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	for _, e := range gmaExts {
		if ext == e {
			return true
		}
	}
	return false
}

// extractPage is a drag-and-drop GMA extractor: drop a file, inspect its
// contents and metadata, verify checksums and extract it.
type extractPage struct {
	a *App

	root *fyne.Container // drop zone or loaded view

	path   string
	info   *gmaops.Info
	verify []bool // nil until verified

	list       *widget.List
	fileName   *widget.Label
	meta       *widget.Form
	dest       *widget.Entry
	verifyBtn  *widget.Button
	verifyBar  *widget.ProgressBar
	verifyInfo *widget.Label
	extractBtn *widget.Button
	loaded     fyne.CanvasObject
	dropZone   fyne.CanvasObject
}

func newExtractPage(a *App) *extractPage {
	e := &extractPage{a: a}

	e.list = widget.NewList(
		func() int {
			if e.info == nil {
				return 0
			}
			return len(e.info.Files)
		},
		func() fyne.CanvasObject {
			// List rows are sized from this template, so give the columns
			// realistic widths: up to 99999 files, sizes like "1023.9 KiB".
			num := widget.NewLabel("99999.")
			num.TextStyle.Monospace = true
			num.Alignment = fyne.TextAlignTrailing
			name := widget.NewLabel("")
			name.Truncation = fyne.TextTruncateEllipsis
			size := widget.NewLabel("1023.9 KiB")
			size.TextStyle.Monospace = true
			size.Alignment = fyne.TextAlignTrailing
			status := widget.NewIcon(nil)
			statusBox := container.NewGridWrap(fyne.NewSquareSize(theme.IconInlineSize()), status)
			return container.NewBorder(nil, nil, num, container.NewHBox(size, container.NewCenter(statusBox)), name)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if e.info == nil || i >= len(e.info.Files) {
				return
			}
			f := e.info.Files[i]
			row := o.(*fyne.Container)
			name := row.Objects[0].(*widget.Label)
			num := row.Objects[1].(*widget.Label)
			right := row.Objects[2].(*fyne.Container)

			digits := len(fmt.Sprint(len(e.info.Files)))
			num.SetText(fmt.Sprintf("%*d.", digits, i+1))
			name.SetText(f.Path)
			right.Objects[0].(*widget.Label).SetText(core.HumanSize(f.Size))

			status := right.Objects[1].(*fyne.Container).Objects[0].(*fyne.Container).Objects[0].(*widget.Icon)
			switch {
			case e.verify == nil || i >= len(e.verify):
				status.SetResource(nil)
			case e.verify[i]:
				status.SetResource(theme.NewSuccessThemedResource(theme.ConfirmIcon()))
			default:
				status.SetResource(theme.NewErrorThemedResource(theme.ErrorIcon()))
			}
		},
	)

	e.fileName = widget.NewLabel("")
	e.fileName.TextStyle.Bold = true
	e.fileName.Truncation = fyne.TextTruncateEllipsis
	e.meta = widget.NewForm()
	e.dest = widget.NewEntry()
	e.dest.SetPlaceHolder("Folder to extract into")

	e.verifyBtn = widget.NewButtonWithIcon("Verify checksums", theme.SearchIcon(), e.runVerify)
	e.verifyBar = widget.NewProgressBar()
	e.verifyBar.TextFormatter = func() string { return "" }
	e.verifyBar.Hide()
	e.verifyInfo = widget.NewLabel("")
	e.verifyInfo.Wrapping = fyne.TextWrapWord

	e.extractBtn = widget.NewButtonWithIcon("Extract", theme.DownloadIcon(), e.run)
	e.extractBtn.Importance = widget.HighImportance

	e.dropZone = e.buildDropZone()
	e.loaded = e.buildLoaded()
	e.root = container.NewStack(e.dropZone)
	return e
}

func (e *extractPage) buildDropZone() fyne.CanvasObject {
	frame := canvas.NewRectangle(color.Transparent)
	frame.StrokeColor = theme.Color(theme.ColorNamePrimary)
	frame.StrokeWidth = 2
	frame.CornerRadius = theme.InputRadiusSize() * 2

	icon := widget.NewIcon(theme.UploadIcon())
	iconBox := container.NewGridWrap(fyne.NewSquareSize(72), icon)

	title := widget.NewLabelWithStyle("Drop a .gma file here", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	hint := widget.NewLabelWithStyle("Legacy .bin addons from the Workshop work too", fyne.TextAlignCenter, fyne.TextStyle{})
	hint.Importance = widget.LowImportance
	browse := widget.NewButtonWithIcon("Browse…", theme.FolderOpenIcon(), e.browse)

	content := container.NewVBox(
		container.NewCenter(iconBox),
		title,
		hint,
		container.NewCenter(browse),
	)
	return container.NewStack(frame, container.NewCenter(content))
}

func (e *extractPage) buildLoaded() fyne.CanvasObject {
	header := container.NewBorder(nil, nil,
		widget.NewIcon(theme.FileIcon()),
		container.NewHBox(
			widget.NewButtonWithIcon("Open another…", theme.FolderOpenIcon(), e.browse),
			widget.NewButtonWithIcon("", theme.CancelIcon(), e.close),
		),
		e.fileName,
	)

	filesTitle := widget.NewLabelWithStyle("Files", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	left := container.NewBorder(filesTitle, nil, nil, nil, e.list)

	verifyRow := container.NewBorder(nil, nil, e.verifyBtn, nil, container.NewStack(e.verifyBar, e.verifyInfo))

	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(0, 56))
	bigExtract := container.NewStack(spacer, e.extractBtn)

	destRow := widget.NewForm(widget.NewFormItem("Extract to", e.a.pathEntry(e.dest, true, nil, nil)))

	right := insetSides(container.NewBorder(
		widget.NewLabelWithStyle("Addon", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewVBox(widget.NewSeparator(), verifyRow, destRow, bigExtract),
		nil, nil,
		container.NewVScroll(e.meta),
	))

	split := container.NewHSplit(left, right)
	split.Offset = 0.55

	return container.NewBorder(container.NewVBox(header, widget.NewSeparator()), nil, nil, nil, split)
}

func (e *extractPage) browse() {
	start := e.path
	if start == "" {
		start = e.a.cfg.Downloads()
	}
	e.a.pickFile(start, gmaExts, func(p string) { e.setGMA(p, "") })
}

func (e *extractPage) close() {
	e.path, e.info, e.verify = "", nil, nil
	e.root.Objects = []fyne.CanvasObject{e.dropZone}
	e.root.Refresh()
}

// setGMA loads a GMA file; dest overrides the default extraction folder.
func (e *extractPage) setGMA(p, dest string) {
	inf, err := gmaops.Inspect(p)
	if err != nil {
		e.a.showError(fmt.Errorf("can't read %s: %w", filepath.Base(p), err))
		return
	}

	e.path, e.info, e.verify = p, inf, nil
	if dest == "" {
		base := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		dest = filepath.Join(e.a.cfg.Uploads(), base)
	}
	e.dest.SetText(dest)
	e.fileName.SetText(filepath.Base(p))
	e.verifyInfo.SetText("")
	e.verifyInfo.Importance = widget.MediumImportance
	e.verifyBtn.Enable()
	e.showMeta()
	e.list.Refresh()
	e.list.ScrollToTop()

	e.root.Objects = []fyne.CanvasObject{e.loaded}
	e.root.Refresh()
}

func (e *extractPage) showMeta() {
	inf := e.info
	var total int64
	for _, f := range inf.Files {
		total += f.Size
	}

	value := func(s string) *widget.Label {
		l := widget.NewLabel(s)
		l.Wrapping = fyne.TextWrapWord
		l.Selectable = true
		return l
	}

	title := widget.NewLabel(inf.Title)
	title.TextStyle.Bold = true
	title.Wrapping = fyne.TextWrapWord

	items := []*widget.FormItem{
		widget.NewFormItem("Title", title),
		widget.NewFormItem("Author", value(inf.Author)),
		widget.NewFormItem("Addon version", value(fmt.Sprint(inf.Version))),
	}
	if inf.Timestamp > 0 {
		items = append(items, widget.NewFormItem("Created", value(time.Unix(inf.Timestamp, 0).Format(time.DateTime))))
	}
	if inf.SteamID != 0 {
		items = append(items, widget.NewFormItem("SteamID", value(fmt.Sprint(inf.SteamID))))
	}
	req := "none"
	if len(inf.RequiredContent) > 0 {
		req = strings.Join(inf.RequiredContent, "\n")
	}
	items = append(items,
		widget.NewFormItem("Required content", value(req)),
		widget.NewFormItem("Files", value(fmt.Sprintf("%d (%s unpacked)", len(inf.Files), core.HumanSize(total)))),
	)
	if st, err := os.Stat(e.path); err == nil {
		items = append(items, widget.NewFormItem("GMA size", value(core.HumanSize(st.Size()))))
	}
	if inf.Legacy {
		items = append(items, widget.NewFormItem("Format", value("Legacy LZMA-compressed addon")))
	}
	items = append(items, widget.NewFormItem("Location", value(e.path)))

	e.meta.Items = items
	e.meta.Refresh()
}

func (e *extractPage) runVerify() {
	if e.path == "" {
		return
	}
	p := e.path
	e.verifyBtn.Disable()
	e.verifyInfo.SetText("")
	e.verifyBar.SetValue(0)
	e.verifyBar.Show()

	go func() {
		var last time.Time
		res, err := gmaops.Verify(p, func(done, total int) {
			// one UI update per file would flood the event queue on big addons
			if done < total && time.Since(last) < 100*time.Millisecond {
				return
			}
			last = time.Now()
			fyne.Do(func() { e.verifyBar.SetValue(float64(done) / float64(total)) })
		})
		fyne.Do(func() {
			e.verifyBar.Hide()
			e.verifyBtn.Enable()
			if p != e.path {
				return // another file was opened meanwhile
			}
			if err != nil {
				e.verifyInfo.Importance = widget.DangerImportance
				e.verifyInfo.SetText("Verify failed: " + err.Error())
				return
			}
			e.verify = res.OK
			if res.Bad == 0 {
				e.verifyInfo.Importance = widget.SuccessImportance
				e.verifyInfo.SetText(fmt.Sprintf("All %d files OK", len(res.OK)))
			} else {
				e.verifyInfo.Importance = widget.DangerImportance
				e.verifyInfo.SetText(fmt.Sprintf("%d of %d files are damaged", res.Bad, len(res.OK)))
			}
			e.verifyInfo.Refresh()
			e.list.Refresh()
		})
	}()
}

func (e *extractPage) run() {
	src := e.path
	dest := strings.TrimSpace(e.dest.Text)
	if src == "" || dest == "" {
		e.a.showError(errors.New("choose a destination folder"))
		return
	}

	var n int
	e.a.runTask("Extract GMA", func(t *task) error {
		var err error
		n, err = e.a.svc.Extract(src, dest, false, t.reporter())
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
	return e.root
}

// handleDrop routes files dropped anywhere on the window: GMA files open in
// the extractor, folders become the Build page's addon folder.
func (a *App) handleDrop(_ fyne.Position, uris []fyne.URI) {
	for _, u := range uris {
		if u.Scheme() != "file" {
			continue
		}
		p := u.Path()
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		switch {
		case !st.IsDir() && isGMAPath(p):
			a.showToolsTab(0)
			a.extract.setGMA(p, "")
			return
		case st.IsDir():
			a.showToolsTab(1)
			a.build.form.sourceDir.SetText(p)
			a.build.form.folderChosen(p)
			return
		}
	}
	dialog.ShowInformation("Unsupported file", "Drop a .gma file to extract it, or an addon folder to build it.", a.win)
}
