package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// sideTabPadScale grows tab button chrome without changing text/icon size.
const sideTabPadScale = 1.18

// sideTabWidth is shared so Workshop and GMA Tools rails match.
var sideTabWidth float32

type sideTab struct {
	Text    string
	Icon    fyne.Resource
	Content fyne.CanvasObject
}

// sideTabs is a leading icon-above-label tab strip with consistent button size.
type sideTabs struct {
	widget.BaseWidget

	Items      []sideTab
	OnSelected func(int)

	selected int
	buttons  []*sideTabButton
	stack    *fyne.Container
}

func newSideTabs(items ...sideTab) *sideTabs {
	t := &sideTabs{Items: items}
	t.ExtendBaseWidget(t)
	t.buttons = make([]*sideTabButton, len(items))
	objs := make([]fyne.CanvasObject, len(items))
	for i, it := range items {
		i := i
		objs[i] = it.Content
		t.buttons[i] = newSideTabButton(it.Text, it.Icon, func() { t.SelectIndex(i) })
	}
	t.stack = container.NewStack(objs...)
	t.applySelection()
	return t
}

func (t *sideTabs) SelectIndex(i int) {
	if i < 0 || i >= len(t.Items) {
		return
	}
	changed := i != t.selected
	t.selected = i
	t.applySelection()
	if changed && t.OnSelected != nil {
		t.OnSelected(i)
	}
}

func (t *sideTabs) SelectedIndex() int { return t.selected }

func (t *sideTabs) applySelection() {
	for i, b := range t.buttons {
		b.setSelected(i == t.selected)
	}
	for i, o := range t.stack.Objects {
		if i == t.selected {
			o.Show()
		} else {
			o.Hide()
		}
	}
	t.stack.Refresh()
}

func (t *sideTabs) CreateRenderer() fyne.WidgetRenderer {
	nav := container.NewVBox()
	for _, b := range t.buttons {
		nav.Add(b)
	}
	bg := canvas.NewRectangle(theme.Color(theme.ColorNameInputBackground))
	bg.CornerRadius = theme.InputRadiusSize()
	rail := container.NewStack(bg, nav)
	root := container.NewBorder(nil, nil, rail, nil, t.stack)
	return widget.NewSimpleRenderer(root)
}

func (t *sideTabs) Refresh() {
	noteSideTabWidth(t.buttonMinWidth())
	for _, b := range t.buttons {
		b.minWidth = sideTabWidth
		b.Refresh()
	}
	t.BaseWidget.Refresh()
}

func (t *sideTabs) buttonMinWidth() float32 {
	var w float32
	for _, b := range t.buttons {
		w = fyne.Max(w, b.naturalWidth())
	}
	return w
}

func noteSideTabWidth(w float32) {
	if w > sideTabWidth {
		sideTabWidth = w
	}
}

type sideTabButton struct {
	widget.BaseWidget

	text      string
	icon      fyne.Resource
	selected  bool
	hovered   bool
	minWidth  float32
	onTapped  func()
}

func newSideTabButton(text string, icon fyne.Resource, onTap func()) *sideTabButton {
	b := &sideTabButton{text: text, icon: icon, onTapped: onTap}
	b.ExtendBaseWidget(b)
	return b
}

func (b *sideTabButton) setSelected(v bool) {
	if b.selected == v {
		return
	}
	b.selected = v
	b.Refresh()
}

func (b *sideTabButton) Tapped(*fyne.PointEvent) {
	if b.onTapped != nil {
		b.onTapped()
	}
}

func (b *sideTabButton) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.Refresh()
}

func (b *sideTabButton) MouseMoved(*desktop.MouseEvent) {}

func (b *sideTabButton) MouseOut() {
	b.hovered = false
	b.Refresh()
}

func (b *sideTabButton) Cursor() desktop.Cursor { return desktop.PointerCursor }

func (b *sideTabButton) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(color.Transparent)
	bg.CornerRadius = theme.SelectionRadiusSize()
	icon := canvas.NewImageFromResource(b.icon)
	icon.FillMode = canvas.ImageFillContain
	label := canvas.NewText(b.text, theme.Color(theme.ColorNameForeground))
	label.Alignment = fyne.TextAlignCenter
	label.TextSize = theme.TextSize()
	r := &sideTabButtonRenderer{b: b, bg: bg, icon: icon, label: label}
	r.Refresh()
	return r
}

func (b *sideTabButton) naturalWidth() float32 {
	th := b.Theme()
	pad := th.Size(theme.SizeNameInnerPadding) * sideTabPadScale
	icon := th.Size(theme.SizeNameInlineIcon) * 2
	label := canvas.NewText(b.text, color.Black)
	label.TextSize = th.Size(theme.SizeNameText)
	return fyne.Max(label.MinSize().Width, icon) + pad
}

func (b *sideTabButton) MinSize() fyne.Size {
	th := b.Theme()
	pad := th.Size(theme.SizeNameInnerPadding) * sideTabPadScale
	icon := th.Size(theme.SizeNameInlineIcon) * 2
	label := canvas.NewText(b.text, color.Black)
	label.TextSize = th.Size(theme.SizeNameText)
	text := label.MinSize()
	h := icon + th.Size(theme.SizeNamePadding) + text.Height + pad*2
	w := fyne.Max(fyne.Max(text.Width, icon)+pad, b.minWidth)
	if sideTabWidth > w {
		w = sideTabWidth
	}
	return fyne.NewSize(w, h)
}

type sideTabButtonRenderer struct {
	b     *sideTabButton
	bg    *canvas.Rectangle
	icon  *canvas.Image
	label *canvas.Text
}

func (r *sideTabButtonRenderer) Destroy() {}

func (r *sideTabButtonRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.icon, r.label}
}

func (r *sideTabButtonRenderer) Layout(size fyne.Size) {
	th := r.b.Theme()
	pad := th.Size(theme.SizeNameInnerPadding) * sideTabPadScale
	gap := th.Size(theme.SizeNamePadding)
	iconSize := th.Size(theme.SizeNameInlineIcon) * 2

	r.bg.Resize(size)
	innerW := size.Width - pad
	x := pad / 2
	y := pad

	r.icon.Resize(fyne.NewSquareSize(iconSize))
	r.icon.Move(fyne.NewPos(x+(innerW-iconSize)/2, y))
	y += iconSize + gap

	labelSize := r.label.MinSize()
	r.label.Resize(fyne.NewSize(innerW, labelSize.Height))
	r.label.Move(fyne.NewPos(x, y))
}

func (r *sideTabButtonRenderer) MinSize() fyne.Size { return r.b.MinSize() }

func (r *sideTabButtonRenderer) Refresh() {
	th := r.b.Theme()
	v := fyne.CurrentApp().Settings().ThemeVariant()

	r.label.Text = r.b.text
	r.label.TextSize = th.Size(theme.SizeNameText)
	r.icon.Resource = r.b.icon

	if r.b.selected {
		r.label.Color = th.Color(theme.ColorNamePrimary, v)
		r.icon.Resource = theme.NewPrimaryThemedResource(r.b.icon)
	} else {
		r.label.Color = th.Color(theme.ColorNameForeground, v)
		r.icon.Resource = theme.NewThemedResource(r.b.icon)
	}

	r.bg.CornerRadius = th.Size(theme.SizeNameSelectionRadius)
	if r.b.hovered {
		r.bg.FillColor = th.Color(theme.ColorNameHover, v)
	} else {
		r.bg.FillColor = color.Transparent
	}
	r.bg.Refresh()
	r.icon.Refresh()
	r.label.Refresh()
	canvas.Refresh(r.b)
}

var (
	_ fyne.Tappable     = (*sideTabButton)(nil)
	_ desktop.Hoverable = (*sideTabButton)(nil)
	_ desktop.Cursorable = (*sideTabButton)(nil)
)
