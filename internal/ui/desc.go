package ui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// descBox is a selectable Workshop description with a Copy action.
// The entry stays visible so Border layouts keep a stable center slot when
// switching items (Hide/Show was letting it jump over the meta block).
type descBox struct {
	a        *App
	entry    *widget.Entry
	btn      *widget.Button
	full     string
	limit    int
	onChange func() // parent should Refresh so the docked layout remeasures
}

func newDescBox(a *App, limit int) *descBox {
	d := &descBox{
		a:     a,
		entry: newSelectableDesc(),
		limit: limit,
	}
	d.btn = widget.NewButtonWithIcon("Copy description", theme.ContentCopyIcon(), d.Copy)
	d.Set("")
	return d
}

// Set updates the text and shows/hides only the Copy button.
func (d *descBox) Set(s string) {
	d.full = s
	d.entry.SetText(truncate(s, d.limit))
	if strings.TrimSpace(s) == "" {
		d.btn.Hide()
	} else {
		d.btn.Show()
	}
	if d.onChange != nil {
		d.onChange()
	}
}

// Copy puts the full description on the clipboard.
func (d *descBox) Copy() {
	if d.full == "" {
		return
	}
	d.a.fa.Clipboard().SetContent(d.full)
}

// Has reports whether a non-empty description is loaded.
func (d *descBox) Has() bool {
	return strings.TrimSpace(d.full) != ""
}

// Entry is the selectable text field.
func (d *descBox) Entry() fyne.CanvasObject { return d.entry }

// Button is the shared Copy description control (hide with the field when empty).
func (d *descBox) Button() *widget.Button { return d.btn }

func newSelectableDesc() *widget.Entry {
	e := widget.NewMultiLineEntry()
	e.Wrapping = fyne.TextWrapWord
	e.SetMinRowsVisible(3)
	return e
}
