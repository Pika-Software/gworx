package ui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Pika-Software/gworx/internal/config"
	"github.com/Pika-Software/gworx/internal/core"
	"github.com/Pika-Software/gworx/internal/gmaops"
	"github.com/Pika-Software/gworx/internal/store"
	"github.com/Pika-Software/gworx/internal/workshop"
)

const (
	modeGMA   = "Upload a finished GMA file"
	modeBuild = "Build the GMA from a folder"
)

type uploadPage struct {
	a *App

	mode    *widget.RadioGroup
	gmaPath *widget.Entry
	gmaInfo *widget.Label
	build   *buildForm
	source  *fyne.Container

	itemID      *widget.Entry
	title       *widget.Entry
	description *widget.Entry
	preview     *widget.Entry
	thumb       *canvas.Image
	visibility  *widget.Select
	changeNote  *widget.Entry

	backendLabel *widget.Label

	// project of the GMA selected in GMA mode, if it was built by GWorX
	gmaProject *gmaops.Project
}

func newUploadPage(a *App) *uploadPage {
	u := &uploadPage{a: a}

	u.gmaPath = widget.NewEntry()
	u.gmaPath.SetPlaceHolder("Path to a .gma file")
	u.gmaInfo = wrapLabel("")
	u.build = newBuildForm(a, false)

	u.itemID = widget.NewEntry()
	u.itemID.SetPlaceHolder("Leave empty to create a new item")

	u.title = widget.NewEntry()
	u.title.SetPlaceHolder("Workshop title (defaults to the addon title)")
	u.description = widget.NewMultiLineEntry()
	u.description.Wrapping = fyne.TextWrapWord
	u.description.SetMinRowsVisible(4)
	u.description.SetPlaceHolder("Workshop description (BBCode supported). Empty keeps the current one on updates.")

	u.preview = widget.NewEntry()
	u.thumb = newThumb(128)
	u.preview.OnChanged = func(string) { u.showThumb() }
	u.itemID.OnChanged = func(string) { u.showThumb() }
	a.previews.subscribe(u.showThumb)
	u.preview.SetPlaceHolder("JPG/PNG/GIF, under 1 MB (required for new items)")

	u.visibility = widget.NewSelect(workshop.VisibilityNames, nil)
	u.visibility.SetSelectedIndex(workshop.VisibilityPrivate)

	u.changeNote = widget.NewMultiLineEntry()
	u.changeNote.SetMinRowsVisible(2)

	u.backendLabel = widget.NewLabel("")
	u.refreshBackendLabel()

	u.source = container.NewStack()
	u.mode = widget.NewRadioGroup([]string{modeGMA, modeBuild}, func(m string) { u.showMode(m) })
	u.mode.Horizontal = true
	u.mode.Required = true
	u.mode.SetSelected(modeBuild)

	return u
}

func (u *uploadPage) refreshBackendLabel() {
	cfg := u.a.cfg
	if cfg.Backend == config.BackendSteamworks {
		u.backendLabel.SetText("Uploading through Steamworks (Steam client must be running).")
	} else if cfg.SteamUsername == "" {
		u.backendLabel.SetText("Uploading through steamcmd: set your Steam username in Settings first.")
	} else {
		u.backendLabel.SetText("Uploading through steamcmd as " + cfg.SteamUsername + ".")
	}
}

func (u *uploadPage) showMode(m string) {
	if m == modeGMA {
		u.source.Objects = []fyne.CanvasObject{widget.NewForm(
			widget.NewFormItem("GMA file", u.a.pathEntry(u.gmaPath, false, []string{".gma"}, u.inspectGMA)),
			widget.NewFormItem("", u.gmaInfo),
		)}
	} else {
		u.source.Objects = []fyne.CanvasObject{container.NewVBox(u.build.toolbar(), u.build.form)}
	}
	u.source.Refresh()
}

func (u *uploadPage) inspectGMA(p string) {
	u.gmaProject = nil
	inf, err := gmaops.Inspect(p)
	if err != nil {
		u.gmaInfo.SetText("Can't read GMA: " + err.Error())
		return
	}
	u.gmaInfo.SetText(fmt.Sprintf("%s by %s, version %d, %d files", inf.Title, inf.Author, inf.Version, len(inf.Files)))
	if u.title.Text == "" {
		u.title.SetText(inf.Title)
	}
}

// showThumb previews the selected image, or the current Workshop image of the item being updated.
func (u *uploadPage) showThumb() {
	if p := strings.TrimSpace(u.preview.Text); p != "" {
		setThumb(u.thumb, u.a.previews.file(p))
		return
	}
	if id, err := workshop.ParseID(u.itemID.Text); err == nil {
		setThumb(u.thumb, u.a.previews.workshop(id))
		return
	}
	setThumb(u.thumb, nil)
}

// useGMA switches to GMA mode with the given file (e.g. right after a build).
func (u *uploadPage) useGMA(p string, project *gmaops.Project) {
	u.mode.SetSelected(modeGMA)
	u.gmaPath.SetText(p)
	u.inspectGMA(p)
	u.gmaProject = project
	if project != nil {
		u.title.SetText(project.Title)
		if project.Description != "" {
			u.description.SetText(project.Description)
		}
	}
}

// loadPublished prepares the page to update an existing item.
func (u *uploadPage) loadPublished(rec store.Published) {
	u.itemID.SetText(strconv.FormatUint(rec.ID, 10))
	u.title.SetText(rec.Title)
	u.description.SetText(rec.Description)
	u.preview.SetText(rec.PreviewPath)
	u.visibility.SetSelectedIndex(rec.Visibility)
	u.changeNote.SetText("")

	if rec.Project != nil {
		u.mode.SetSelected(modeBuild)
		u.build.setProject(rec.Project)
		if rec.Project.Version > 0 {
			u.build.version.SetText(strconv.Itoa(int(rec.Project.Version) + 1))
		}
	} else if rec.GMAPath != "" {
		u.mode.SetSelected(modeGMA)
		u.gmaPath.SetText(rec.GMAPath)
		u.inspectGMA(rec.GMAPath)
	}
}

func (u *uploadPage) run() {
	var id uint64
	if s := strings.TrimSpace(u.itemID.Text); s != "" {
		var err error
		if id, err = workshop.ParseID(s); err != nil {
			u.a.showError(err)
			return
		}
	}

	req := workshop.UploadRequest{
		ID:          id,
		Title:       strings.TrimSpace(u.title.Text),
		Description: u.description.Text,
		PreviewPath: strings.TrimSpace(u.preview.Text),
		ChangeNote:  u.changeNote.Text,
		Visibility:  max(u.visibility.SelectedIndex(), 0),
	}

	var project *gmaops.Project
	var out string
	buildMode := u.mode.Selected == modeBuild

	if buildMode {
		p, err := u.build.project()
		if err != nil {
			u.a.showError(err)
			return
		}
		project = p
		out = u.build.outputPath(p)
		req.GMAPath = out
	} else {
		req.GMAPath = strings.TrimSpace(u.gmaPath.Text)
		if req.GMAPath == "" {
			u.a.showError(errors.New("choose a GMA file"))
			return
		}
		project = u.gmaProject
	}
	if id == 0 && req.PreviewPath == "" {
		u.a.showError(errors.New("new Workshop items need a preview image"))
		return
	}

	if req.Title == "" && project != nil {
		req.Title = project.Title
	}
	confirm := fmt.Sprintf("Upload %q as a new Workshop item?", req.Title)
	if id != 0 {
		confirm = fmt.Sprintf("Update Workshop item %d (%s)?", id, req.Title)
	}

	dialog.ShowConfirm("Upload", confirm, func(ok bool) {
		if !ok {
			return
		}
		var newID uint64
		u.a.runTask("Upload to Workshop", func(t *task) error {
			var err error
			newID, err = u.a.svc.Upload(t.ctx, core.UploadOptions{Request: req, Project: project, Build: buildMode}, t.reporter())
			return err
		}, func() {
			u.itemID.SetText(strconv.FormatUint(newID, 10))
			u.changeNote.SetText("")
			dialog.NewCustomConfirm("Uploaded", "Open in browser", "Close",
				wrapLabel(fmt.Sprintf("Workshop item %d is up to date.\nIf it is new, check the visibility and accept the Workshop agreement on the page.", newID)),
				func(open bool) {
					if open {
						u.a.openURL(workshop.ItemURL(newID))
					}
				}, u.a.win).Show()
		})
	}, u.a.win)
}

func (u *uploadPage) object() fyne.CanvasObject {
	workshopForm := widget.NewForm(
		widget.NewFormItem("Workshop item", u.itemID),
		widget.NewFormItem("Title", u.title),
		widget.NewFormItem("Description", u.description),
		&widget.FormItem{Text: "Preview image", Widget: container.NewVBox(
			u.a.pathEntry(u.preview, false, []string{".jpg", ".jpeg", ".png", ".gif"}, nil),
			container.NewHBox(u.thumb),
		), HintText: "Leave empty when updating to keep the current Workshop image"},
		widget.NewFormItem("Visibility", u.visibility),
		widget.NewFormItem("Change note", u.changeNote),
	)

	newBtn := widget.NewButtonWithIcon("New item", theme.ContentAddIcon(), func() {
		u.itemID.SetText("")
		u.title.SetText("")
		u.description.SetText("")
		u.changeNote.SetText("")
	})

	uploadBtn := widget.NewButtonWithIcon("Upload", theme.UploadIcon(), u.run)
	uploadBtn.Importance = widget.HighImportance

	body := container.NewVBox(
		card("Source", "", framePad(container.NewVBox(u.mode, u.source))),
		card("Workshop", "", framePad(workshopForm)),
	)

	return container.NewBorder(nil, container.NewBorder(nil, nil, nil, container.NewHBox(newBtn, uploadBtn), u.backendLabel), nil, nil,
		container.NewVScroll(body))
}
