package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	maxConsoleHistory  = 100
	maxLearnedCommands = 4096
	// maxConsoleCommand is how long a line typed into the server console may be.
	// Longer pastes are cut so the field cannot grow without a bound.
	maxConsoleCommand = 1024
)

// consoleEntry is the dedicated-server command box.
// Up/Down walks commands that were sent. Tab cycles suggestions.
type consoleEntry struct {
	widget.Entry

	hist    *cmdHist
	names   func() []string
	suggest *suggestList

	suppress bool
	picking  bool
	cycle    int
	base     string
}

func newConsoleEntry(names func() []string) *consoleEntry {
	e := &consoleEntry{
		names: names,
		hist:  &cmdHist{browse: -1},
		cycle: -1,
	}
	e.ExtendBaseWidget(e)
	e.OnChanged = e.changed
	return e
}

// AcceptsTab keeps Tab for completion instead of moving focus.
func (e *consoleEntry) AcceptsTab() bool { return true }

func (e *consoleEntry) TypedKey(key *fyne.KeyEvent) {
	switch key.Name {
	case fyne.KeyUp:
		if s, ok := e.hist.up(); ok {
			e.showHistory(s)
		}
	case fyne.KeyDown:
		if s, ok := e.hist.down(); ok {
			e.showHistory(s)
		}
	case fyne.KeyEscape:
		e.cancelBrowse()
	case fyne.KeyTab:
		mods := keyMods()
		if mods&^fyne.KeyModifierShift != 0 {
			return
		}
		e.complete(mods&fyne.KeyModifierShift != 0)
	case fyne.KeyReturn, fyne.KeyEnter:
		e.dismissSuggest()
		e.Entry.TypedKey(key)
	default:
		e.Entry.TypedKey(key)
	}
}

func (e *consoleEntry) FocusLost() {
	e.Entry.FocusLost()
	// A click on a suggestion unfocuses the entry before the click returns.
	// Leave the list up so that click can fill the line.
	if e.picking {
		return
	}
	e.dismissSuggest()
}

func (e *consoleEntry) changed(s string) {
	if e.suppress {
		return
	}
	if trimmed := limitCommand(s); trimmed != s {
		e.applyLine(trimmed)
		s = trimmed
	}
	e.hist.edit(s)
	e.cycle = -1
	e.base = ""
	e.refreshSuggest()
}

func (e *consoleEntry) showHistory(s string) {
	e.cycle = -1
	e.base = ""
	e.applyLine(s)
	e.refreshSuggest()
}

func (e *consoleEntry) applyPicked(s string) {
	e.cycle = -1
	e.base = ""
	e.hist.edit(s)
	e.applyLine(s)
	e.refreshSuggest()
}

func (e *consoleEntry) applyLine(s string) {
	s = limitCommand(s)
	e.suppress = true
	e.CursorRow = 0
	e.CursorColumn = 1 << 30
	e.SetText(s)
	e.suppress = false
}

func limitCommand(s string) string {
	if utf8.RuneCountInString(s) <= maxConsoleCommand {
		return s
	}
	return string([]rune(s)[:maxConsoleCommand])
}

func (e *consoleEntry) complete(back bool) {
	base := e.Text
	if e.cycle >= 0 {
		base = e.base
	}
	matches := e.suggestions(base)
	if len(matches) == 0 {
		return
	}
	if e.cycle < 0 {
		e.base = base
		e.cycle = 0
	} else if back {
		e.cycle--
		if e.cycle < 0 {
			e.cycle = len(matches) - 1
		}
	} else {
		e.cycle = (e.cycle + 1) % len(matches)
	}
	e.applyLine(matches[e.cycle])
	e.refreshSuggest()
}

func (e *consoleEntry) cancelBrowse() {
	if e.cycle >= 0 {
		base := e.base
		e.cycle = -1
		e.base = ""
		e.applyLine(base)
		e.refreshSuggest()
		return
	}
	if e.suggest != nil && len(e.suggest.items) > 0 {
		e.suggest.set(nil, -1)
		return
	}
	if s, ok := e.hist.cancel(); ok {
		e.showHistory(s)
	}
}

func (e *consoleEntry) dismissSuggest() {
	e.cycle = -1
	e.base = ""
	if e.suggest != nil {
		e.suggest.set(nil, -1)
	}
}

func (e *consoleEntry) suggestions(line string) []string {
	var learned []string
	if e.names != nil {
		learned = e.names()
	}
	return suggestCommands(line, e.hist.items, learned)
}

func (e *consoleEntry) refreshSuggest() {
	if e.suggest == nil {
		return
	}
	src := e.Text
	hot := 0
	if e.cycle >= 0 {
		src = e.base
		hot = e.cycle
	}
	matches := e.suggestions(src)
	if hot >= len(matches) {
		hot = 0
	}
	e.suggest.set(matches, hot)
}

func keyMods() fyne.KeyModifier {
	app := fyne.CurrentApp()
	if app == nil || app.Driver() == nil {
		return 0
	}
	d, ok := app.Driver().(desktop.Driver)
	if !ok {
		return 0
	}
	return d.CurrentKeyModifiers()
}

// cmdHist is the sent-command list for one entry.
// browse < 0 means the field is editing draft; otherwise it is an index into items.
type cmdHist struct {
	items  []string
	browse int
	prefix string
	draft  string
}

func (h *cmdHist) up() (string, bool) {
	start := h.browse
	prefix := h.prefix
	if h.browse < 0 {
		prefix = h.draft
		start = len(h.items)
	}
	for i := start - 1; i >= 0; i-- {
		if hasPrefixFold(h.items[i], prefix) {
			if h.browse < 0 {
				h.prefix = prefix
			}
			h.browse = i
			return h.items[i], true
		}
	}
	return "", false
}

func (h *cmdHist) down() (string, bool) {
	if h.browse < 0 {
		return "", false
	}
	for i := h.browse + 1; i < len(h.items); i++ {
		if hasPrefixFold(h.items[i], h.prefix) {
			h.browse = i
			return h.items[i], true
		}
	}
	h.browse = -1
	h.prefix = ""
	return h.draft, true
}

func (h *cmdHist) cancel() (string, bool) {
	if h.browse < 0 {
		return "", false
	}
	h.browse = -1
	h.prefix = ""
	return h.draft, true
}

func (h *cmdHist) edit(text string) {
	h.browse = -1
	h.prefix = ""
	h.draft = text
}

func (h *cmdHist) push(cmd string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return
	}
	for i, it := range h.items {
		if it == cmd {
			h.items = append(h.items[:i], h.items[i+1:]...)
			break
		}
	}
	h.items = append(h.items, cmd)
	if len(h.items) > maxConsoleHistory {
		h.items = h.items[len(h.items)-maxConsoleHistory:]
	}
	h.edit("")
}

// consoleNames collects cvar and command names seen in server output.
type consoleNames struct {
	mu  sync.Mutex
	set map[string]struct{}
}

func (n *consoleNames) add(name string) {
	if n == nil || name == "" {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.set == nil {
		n.set = map[string]struct{}{}
	}
	if _, ok := n.set[name]; ok {
		return
	}
	if len(n.set) >= maxLearnedCommands {
		return
	}
	n.set[name] = struct{}{}
}

func (n *consoleNames) list() []string {
	if n == nil {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]string, 0, len(n.set))
	for name := range n.set {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

type consoleHistoryFile struct {
	Commands map[string][]string `json:"commands"`
}

// consoleHistoryStore persists sent commands per server id.
type consoleHistoryStore struct {
	mu   sync.Mutex
	path string
	cmds map[string][]string
}

func loadConsoleHistory(path string) *consoleHistoryStore {
	s := &consoleHistoryStore{path: path, cmds: map[string][]string{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var file consoleHistoryFile
	if json.Unmarshal(raw, &file) != nil || file.Commands == nil {
		return s
	}
	s.cmds = file.Commands
	return s
}

func (s *consoleHistoryStore) list(id string) []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.cmds[id]...)
}

func (s *consoleHistoryStore) put(id string, items []string) {
	if s == nil || id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmds == nil {
		s.cmds = map[string][]string{}
	}
	s.cmds[id] = append([]string(nil), items...)
	raw, err := json.MarshalIndent(consoleHistoryFile{Commands: s.cmds}, "", "\t")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, s.path)
}

// suggestList is the completion list drawn above the command box.
type suggestList struct {
	widget.BaseWidget
	items  []string
	hot    int
	onPick func(string)
}

func newSuggestList(onPick func(string)) *suggestList {
	s := &suggestList{onPick: onPick, hot: -1}
	s.ExtendBaseWidget(s)
	s.Hide()
	return s
}

func (s *suggestList) set(items []string, hot int) {
	if len(items) == 0 {
		hot = -1
	} else if hot < 0 || hot >= len(items) {
		hot = 0
	}
	if slices.Equal(s.items, items) && s.hot == hot {
		return
	}
	s.items = append([]string(nil), items...)
	s.hot = hot
	if len(s.items) == 0 {
		s.Hide()
	} else {
		s.Show()
	}
	s.Refresh()
}

func (s *suggestList) MinSize() fyne.Size {
	s.ExtendBaseWidget(s)
	if len(s.items) == 0 {
		return fyne.NewSize(0, 0)
	}
	return fyne.NewSize(0, s.rowH()*float32(len(s.items)))
}

func (s *suggestList) rowH() float32 { return consoleLineHeight() }

func (s *suggestList) indexAt(y float32) int {
	h := s.rowH()
	if h < 1 || len(s.items) == 0 {
		return -1
	}
	i := int(y / h)
	if i < 0 || i >= len(s.items) {
		return -1
	}
	return i
}

func (s *suggestList) MouseDown(e *desktop.MouseEvent) {
	if e.Button != desktop.MouseButtonPrimary || s.onPick == nil {
		return
	}
	i := s.indexAt(e.Position.Y)
	if i < 0 {
		return
	}
	s.onPick(s.items[i])
}

func (s *suggestList) MouseUp(*desktop.MouseEvent) {}

func (s *suggestList) Cursor() desktop.Cursor { return desktop.PointerCursor }

func (s *suggestList) CreateRenderer() fyne.WidgetRenderer {
	return &suggestRenderer{
		list: s,
		bg:   canvas.NewRectangle(theme.Color(theme.ColorNameInputBackground)),
		root: container.NewWithoutLayout(),
	}
}

type suggestRenderer struct {
	list *suggestList
	bg   *canvas.Rectangle
	rows []*canvas.Text
	hot  []*canvas.Rectangle
	root *fyne.Container
}

func (r *suggestRenderer) Destroy() {}

func (r *suggestRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.root}
}

func (r *suggestRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	r.place(size)
}

func (r *suggestRenderer) MinSize() fyne.Size { return r.list.MinSize() }

func (r *suggestRenderer) Refresh() {
	th := r.list.Theme()
	v := fyne.CurrentApp().Settings().ThemeVariant()
	r.bg.FillColor = th.Color(theme.ColorNameInputBackground, v)
	r.bg.CornerRadius = th.Size(theme.SizeNameInputRadius)
	r.place(r.list.Size())
	canvas.Refresh(r.list)
}

func (r *suggestRenderer) place(size fyne.Size) {
	s := r.list
	n := len(s.items)
	for len(r.rows) < n {
		t := canvas.NewText("", theme.Color(theme.ColorNameForeground))
		t.TextStyle = fyne.TextStyle{Monospace: true}
		r.rows = append(r.rows, t)
		r.hot = append(r.hot, canvas.NewRectangle(theme.Color(theme.ColorNameSelection)))
	}
	rowH := s.rowH()
	pad := theme.InnerPadding()
	maxW := size.Width - pad*2
	if maxW < 1 {
		maxW = 1
	}
	th := s.Theme()
	variant := fyne.CurrentApp().Settings().ThemeVariant()
	fg := th.Color(theme.ColorNameForeground, variant)
	sel := th.Color(theme.ColorNameSelection, variant)
	objs := make([]fyne.CanvasObject, 0, 1+n*2)
	r.bg.Resize(size)
	objs = append(objs, r.bg)
	for i := 0; i < n; i++ {
		mark := r.hot[i]
		text := r.rows[i]
		y := float32(i) * rowH
		if i == s.hot {
			mark.FillColor = sel
			mark.Move(fyne.NewPos(0, y))
			mark.Resize(fyne.NewSize(size.Width, rowH))
			mark.Show()
			objs = append(objs, mark)
		} else {
			mark.Hide()
		}
		text.Text = fitConsoleText(s.items[i], maxW)
		text.TextSize = th.Size(theme.SizeNameText)
		text.Color = fg
		textH := text.MinSize().Height
		text.Move(fyne.NewPos(pad, y+(rowH-textH)/2))
		text.Show()
		objs = append(objs, text)
	}
	for i := n; i < len(r.rows); i++ {
		r.rows[i].Hide()
		r.hot[i].Hide()
	}
	r.root.Objects = objs
	r.root.Refresh()
}

func fitConsoleText(s string, maxW float32) string {
	if maxW <= 0 || monoWidth(s) <= maxW {
		return s
	}
	runes := []rune(s)
	for len(runes) > 1 && monoWidth(string(runes)+"…") > maxW {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

var (
	_ fyne.Tabbable      = (*consoleEntry)(nil)
	_ fyne.Focusable     = (*consoleEntry)(nil)
	_ desktop.Mouseable  = (*suggestList)(nil)
	_ desktop.Cursorable = (*suggestList)(nil)
)
