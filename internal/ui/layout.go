package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// insetLayout gives its children a fixed margin on the selected sides.
type insetLayout struct {
	top, right, bottom, left float32
}

func (l insetLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	pos := fyne.NewPos(l.left, l.top)
	inner := fyne.NewSize(size.Width-l.left-l.right, size.Height-l.top-l.bottom)
	for _, o := range objs {
		o.Move(pos)
		o.Resize(inner)
	}
}

func (l insetLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	min := fyne.NewSize(0, 0)
	for _, o := range objs {
		min = min.Max(o.MinSize())
	}
	return min.AddWidthHeight(l.left+l.right, l.top+l.bottom)
}

func pagePadding() float32 {
	return theme.InnerPadding() + theme.Padding()
}

// inset pads a page on every side.
func inset(o fyne.CanvasObject) *fyne.Container {
	p := pagePadding()
	return container.New(insetLayout{p, p, p, p}, o)
}

// insetSides pads a pane horizontally, e.g. the details side of a split.
func insetSides(o fyne.CanvasObject) *fyne.Container {
	p := pagePadding()
	return container.New(insetLayout{0, p, 0, p}, o)
}

// greyPanel is a rounded input-background panel with inner padding.
func greyPanel(o fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(theme.Color(theme.ColorNameInputBackground))
	bg.CornerRadius = theme.InputRadiusSize()
	p := theme.InnerPadding()
	return container.NewStack(bg, container.New(insetLayout{p, p, p, p}, o))
}

// framePad adds inner padding inside a card/frame.
func framePad(o fyne.CanvasObject) fyne.CanvasObject {
	p := theme.InnerPadding() + theme.Padding()
	return container.New(insetLayout{p, p, p, p}, o)
}

// card is a widget.Card whose content is padded to line up with the title
// (Fyne indents the title by one padding more than the content).
func card(title, subtitle string, content fyne.CanvasObject) *widget.Card {
	p := theme.Padding()
	return widget.NewCard(title, subtitle, container.New(insetLayout{0, p, p, p}, content))
}
