package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Pika-Software/gworx/internal/store"
	"github.com/Pika-Software/gworx/internal/workshop"
)

// minePage lists the addons the user has published.
type minePage struct {
	a *App

	items    []store.Published
	selected int
	list     *widget.List
	details  *widget.Label
	desc     *descBox
	image    *thumb
	imageBox *fyne.Container
	detail   *fyne.Container // image + meta on top, description fills the rest
	actions  *fyne.Container
}

func newMinePage(a *App) *minePage {
	m := &minePage{a: a, selected: -1}

	m.list = widget.NewList(
		func() int { return len(m.items) },
		func() fyne.CanvasObject {
			title := widget.NewLabel("")
			title.TextStyle.Bold = true
			title.Truncation = fyne.TextTruncateEllipsis
			sub := widget.NewLabel("")
			sub.Truncation = fyne.TextTruncateEllipsis
			return container.NewBorder(nil, nil, newThumb(56), nil, container.NewVBox(title, sub))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			it := m.items[i]
			row := o.(*fyne.Container)
			box := row.Objects[0].(*fyne.Container)
			setThumb(row.Objects[1].(*thumb), m.preview(it))
			box.Objects[0].(*widget.Label).SetText(it.Title)
			sub := fmt.Sprintf("%d", it.ID)
			if !it.UpdatedAt.IsZero() {
				sub += "  ·  " + it.UpdatedAt.Format(time.DateOnly)
			}
			box.Objects[1].(*widget.Label).SetText(sub)
		},
	)
	m.list.OnSelected = func(i widget.ListItemID) {
		m.selected = i
		m.showDetails()
	}
	m.list.OnUnselected = func(widget.ListItemID) {
		m.selected = -1
		m.showDetails()
	}

	m.details = wrapLabel("")
	m.desc = newDescBox(a, 800)
	m.image = newThumb(192)
	m.image.Hide()
	m.imageBox = container.NewHBox(m.image)
	m.detail = container.NewBorder(container.NewVBox(m.imageBox, m.details), nil, nil, nil, m.desc.Entry())
	m.desc.onChange = func() { m.detail.Refresh() }
	m.actions = container.NewHBox()
	a.previews.subscribe(func() {
		m.list.Refresh()
		m.showImage()
	})
	m.reload()
	return m
}

// preview prefers the local preview image used for the last upload.
func (m *minePage) preview(it store.Published) *preview {
	if it.PreviewPath != "" {
		if p := m.a.previews.file(it.PreviewPath); p != nil {
			return p
		}
	}
	return m.a.previews.workshop(it.ID)
}

func (m *minePage) showImage() {
	it, ok := m.current()
	if !ok {
		m.image.Hide()
		m.detail.Refresh()
		return
	}
	setThumb(m.image, m.preview(it))
	m.image.Show()
	m.detail.Refresh()
}

func (m *minePage) current() (store.Published, bool) {
	if m.selected < 0 || m.selected >= len(m.items) {
		return store.Published{}, false
	}
	return m.items[m.selected], true
}

func (m *minePage) reload() {
	var keep uint64
	if cur, ok := m.current(); ok {
		keep = cur.ID
	}

	m.items = m.a.store.Published()
	m.selected = -1
	m.list.UnselectAll()
	m.list.Refresh()

	for i, it := range m.items {
		if it.ID == keep && keep != 0 {
			m.list.Select(i)
			return
		}
	}
	m.showDetails()
}

func (m *minePage) showDetails() {
	m.showImage()
	it, ok := m.current()
	if !ok {
		m.details.SetText("Addons you upload with GWorX appear here. Use \"Refresh from Steam\" to import the ones you published before.")
		m.desc.Set("")
		m.detail.Refresh()
		m.actions.Objects = nil
		m.actions.Refresh()
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\nWorkshop ID: %d\n", it.Title, it.ID)
	if !it.UpdatedAt.IsZero() {
		fmt.Fprintf(&b, "Last updated: %s\n", it.UpdatedAt.Format(time.DateTime))
	}
	if it.Visibility >= 0 && it.Visibility < len(workshop.VisibilityNames) {
		fmt.Fprintf(&b, "Visibility: %s\n", workshop.VisibilityNames[it.Visibility])
	}
	if it.Project != nil {
		fmt.Fprintf(&b, "Source folder: %s\nAddon version: %d\n", it.Project.SourceDir, it.Project.Version)
	}
	if it.GMAPath != "" {
		fmt.Fprintf(&b, "Last GMA: %s\n", it.GMAPath)
	}
	m.details.SetText(b.String())
	m.desc.Set(it.Description)
	m.detail.Refresh()

	update := widget.NewButtonWithIcon("Update…", theme.UploadIcon(), func() {
		m.a.upload.loadPublished(it)
		m.a.showWorkshopTab(1)
	})
	update.Importance = widget.HighImportance

	acts := []fyne.CanvasObject{
		update,
		widget.NewButtonWithIcon("Open on Workshop", theme.ComputerIcon(), func() { m.a.openURL(workshop.ItemURL(it.ID)) }),
	}
	if m.desc.Has() {
		acts = append(acts, m.desc.Button())
	}
	acts = append(acts,
		widget.NewButtonWithIcon("Delete from Workshop", theme.DeleteIcon(), func() { m.deleteItem(it) }),
		widget.NewButtonWithIcon("Forget", theme.CancelIcon(), func() {
			dialog.ShowConfirm("Forget addon", fmt.Sprintf("Remove %q from this list? The Workshop item is not touched.", it.Title), func(ok bool) {
				if ok {
					if err := m.a.store.RemovePublished(it.ID); err != nil {
						m.a.showError(err)
					}
				}
			}, m.a.win)
		}),
	)
	m.actions.Objects = acts
	m.actions.Refresh()
}

func (m *minePage) deleteItem(it store.Published) {
	dialog.ShowConfirm("Delete from Workshop",
		fmt.Sprintf("Permanently delete Workshop item %d (%s)?\nThis can't be undone.", it.ID, it.Title),
		func(ok bool) {
			if !ok {
				return
			}
			m.a.runTask("Delete Workshop item", func(t *task) error {
				return m.a.svc.Delete(t.ctx, it.ID, t.reporter())
			}, nil)
		}, m.a.win)
}

func (m *minePage) refresh() {
	var count int
	m.a.runTask("Refresh published addons", func(t *task) error {
		items, err := m.a.svc.RefreshPublished(t.ctx, t.reporter())
		count = len(items)
		return err
	}, func() {
		dialog.ShowInformation("Refreshed", fmt.Sprintf("Found %d published items.", count), m.a.win)
	})
}

func (m *minePage) addByID() {
	entry := widget.NewEntry()
	entry.SetPlaceHolder("Workshop link or ID")
	dialog.ShowForm("Add existing addon", "Add", "Cancel", []*widget.FormItem{widget.NewFormItem("Item", entry)}, func(ok bool) {
		if !ok {
			return
		}
		id, err := workshop.ParseID(entry.Text)
		if err != nil {
			m.a.showError(err)
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			_, err := m.a.svc.AddPublished(ctx, id)
			if err != nil {
				fyne.Do(func() { m.a.showError(err) })
			}
		}()
	}, m.a.win)
}

func (m *minePage) object() fyne.CanvasObject {
	toolbar := container.NewHBox(
		widget.NewButtonWithIcon("Refresh from Steam", theme.ViewRefreshIcon(), m.refresh),
		widget.NewButtonWithIcon("Add by ID", theme.ContentAddIcon(), m.addByID),
		widget.NewButtonWithIcon("New addon", theme.UploadIcon(), func() {
			m.a.upload.itemID.SetText("")
			m.a.showWorkshopTab(1)
		}),
	)

	right := insetSides(container.NewBorder(nil, container.NewHScroll(m.actions), nil, nil,
		greyPanel(m.detail)))
	split := container.NewHSplit(m.list, right)
	split.Offset = 0.4

	return container.NewBorder(toolbar, nil, nil, nil, split)
}
