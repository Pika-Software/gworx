package ui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// descBox shows a Workshop description. The text can be selected, not edited,
// and it scrolls: the full string is kept, including text that does not wrap
// on spaces.
type descBox struct {
	view     *descScroll
	onChange func() // parent should Refresh so the docked layout remeasures
}

func newDescBox() *descBox {
	d := &descBox{view: newDescScroll()}
	d.Set("")
	return d
}

// Set shows s in full. Carriage returns and a trailing partial UTF-8 sequence
// (Steam cuts descriptions on a byte boundary) are cleaned up first.
func (d *descBox) Set(s string) {
	d.view.setText(normalizeDesc(s))
	if d.onChange != nil {
		d.onChange()
	}
}

// Object is the scrolling text view.
func (d *descBox) Object() fyne.CanvasObject { return d.view }

func normalizeDesc(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ToValidUTF8(s, "\uFFFD")
}

// descScroll is a selectable label in a vertical scroll. The label is sized to
// the viewport width so wrapping is computed there, then grown to the wrapped
// height. A plain scroll uses the unwrapped minimum, which clips every line
// past the first for text that has no spaces (and the tail of a long paragraph).
type descScroll struct {
	widget.BaseWidget

	label  *widget.Label
	scroll *container.Scroll
}

func newDescScroll() *descScroll {
	l := widget.NewLabel("")
	l.Wrapping = fyne.TextWrapWord
	l.Truncation = fyne.TextTruncateOff
	l.Selectable = true
	s := &descScroll{
		label:  l,
		scroll: container.NewVScroll(l),
	}
	s.ExtendBaseWidget(s)
	return s
}

func (s *descScroll) setText(text string) {
	s.label.SetText(text)
	s.Refresh()
}

func (s *descScroll) CreateRenderer() fyne.WidgetRenderer {
	return &descScrollRenderer{s: s, scroll: s.scroll}
}

func (s *descScroll) MinSize() fyne.Size {
	s.ExtendBaseWidget(s)
	th := s.Theme()
	line := th.Size(theme.SizeNameText) + th.Size(theme.SizeNameLineSpacing)
	return fyne.NewSize(120, line*4)
}

type descScrollRenderer struct {
	s      *descScroll
	scroll *container.Scroll
}

func (r *descScrollRenderer) Destroy() {}

func (r *descScrollRenderer) Layout(size fyne.Size) {
	w := size.Width
	if w < 1 {
		w = 1
	}
	// Width first, so MinSize height is the height of the wrapped text.
	r.s.label.Resize(fyne.NewSize(w, size.Height))
	h := r.s.label.MinSize().Height
	if h < size.Height {
		h = size.Height
	}
	r.s.label.Resize(fyne.NewSize(w, h))
	r.scroll.Resize(size)
}

func (r *descScrollRenderer) MinSize() fyne.Size { return r.s.MinSize() }

func (r *descScrollRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.scroll}
}

func (r *descScrollRenderer) Refresh() {
	r.scroll.Refresh()
}
