package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Custom theme size names (looked up by GWorX widgets).
const (
	// SizeNameSideTabPad is inner padding for leading side-tab buttons (~18% roomier).
	SizeNameSideTabPad fyne.ThemeSizeName = "gworx.sideTabPad"
)

// Brand accent: teal (workshop/tool feel, not the default blue/purple).
var (
	colorPrimary     = color.NRGBA{R: 0x2A, G: 0xB8, B: 0xA0, A: 0xff}
	colorPrimaryDark = color.NRGBA{R: 0x1F, G: 0x9A, B: 0x86, A: 0xff} // light variant: stronger on white
	colorOnPrimary   = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}

	colorInputDark  = color.NRGBA{R: 0x2A, G: 0x2A, B: 0x2E, A: 0xff}
	colorInputLight = color.NRGBA{R: 0xEE, G: 0xEF, B: 0xF1, A: 0xff}
)

// AppTheme wraps Fyne's default theme with GWorX colours and radii.
func AppTheme() fyne.Theme {
	return &gworxTheme{Theme: theme.DefaultTheme()}
}

type gworxTheme struct {
	fyne.Theme
}

func (t *gworxTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	primary := colorPrimary
	if v == theme.VariantLight {
		primary = colorPrimaryDark
	}

	switch n {
	case theme.ColorNamePrimary, theme.ColorNameHyperlink:
		return primary
	case theme.ColorNameForegroundOnPrimary:
		return colorOnPrimary
	case theme.ColorNameFocus:
		r, g, b, _ := primary.RGBA()
		return color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0x40}
	case theme.ColorNameSelection:
		r, g, b, _ := primary.RGBA()
		return color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0x55}
	case theme.ColorNameInputBackground:
		if v == theme.VariantLight {
			return colorInputLight
		}
		return colorInputDark
	default:
		return t.Theme.Color(n, v)
	}
}

func (t *gworxTheme) Font(s fyne.TextStyle) fyne.Resource {
	// Keep default (and FYNE_FONT / FYNE_FONT_MONOSPACE) so logs/console monospace stays sharp.
	return t.Theme.Font(s)
}

func (t *gworxTheme) Icon(n fyne.ThemeIconName) fyne.Resource {
	return t.Theme.Icon(n)
}

func (t *gworxTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameInputRadius, theme.SizeNameSelectionRadius:
		return 6
	case SizeNameSideTabPad:
		return t.Theme.Size(theme.SizeNameInnerPadding) * 1.18
	default:
		return t.Theme.Size(n)
	}
}

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
