package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/unknown-gd/go-gma"

	"github.com/Pika-Software/gworx/internal/gmaops"
	"github.com/Pika-Software/gworx/internal/workshop"
)

// buildForm edits a gmaops.Project. It is used by the Build page and the Upload page.
type buildForm struct {
	a *App

	title       *widget.Entry
	description *widget.Entry
	author      *widget.Entry
	version     *widget.Entry
	category    *widget.Select
	tags        *tagPicker
	required    *widget.Entry
	sourceDir   *widget.Entry
	ignore      *widget.Entry
	output      *widget.Entry

	withOutput bool
	form       *widget.Form
}

func newBuildForm(a *App, withOutput bool) *buildForm {
	f := &buildForm{a: a, withOutput: withOutput}

	f.title = widget.NewEntry()
	f.title.SetPlaceHolder("My Addon")

	f.description = widget.NewMultiLineEntry()
	f.description.SetMinRowsVisible(3)
	f.description.Wrapping = fyne.TextWrapWord
	f.description.SetPlaceHolder("Shown on the Workshop page")

	f.author = widget.NewEntry()
	f.author.SetText(a.defaultAuthor())

	f.version = widget.NewEntry()
	f.version.SetText("1")
	f.version.Validator = func(s string) error {
		if v, err := strconv.Atoi(strings.TrimSpace(s)); err != nil || v < 1 {
			return errors.New("must be a whole number ≥ 1")
		}
		return nil
	}

	f.category = widget.NewSelect(gma.CategoryList, nil)
	f.category.PlaceHolder = "(select addon type)"

	f.tags = newTagPicker(gma.TagList, 3)

	f.required = widget.NewMultiLineEntry()
	f.required.SetMinRowsVisible(2)
	f.required.SetPlaceHolder("One Workshop ID or link per line")

	f.sourceDir = widget.NewEntry()
	f.sourceDir.SetPlaceHolder("Folder that contains lua/, materials/, models/ ...")

	f.ignore = widget.NewMultiLineEntry()
	f.ignore.SetMinRowsVisible(4)
	f.ignore.SetText(strings.Join(a.cfg.DefaultIgnore, "\n"))

	f.output = widget.NewEntry()
	f.output.SetPlaceHolder("Default: " + filepath.Join(a.cfg.Builds(), "<title>.gma"))

	items := []*widget.FormItem{
		widget.NewFormItem("Title", f.title),
		widget.NewFormItem("Description", f.description),
		widget.NewFormItem("Author", f.author),
		widget.NewFormItem("Addon version", f.version),
		widget.NewFormItem("Type", f.category),
		{Text: "Tags", Widget: f.tags.grid, HintText: "1 to 3 tags"},
		{Text: "Required content", Widget: f.required, HintText: "Workshop addons this addon depends on"},
		widget.NewFormItem("Addon folder", a.pathEntry(f.sourceDir, true, nil, f.folderChosen)),
		{Text: "Ignore", Widget: f.ignore, HintText: "Wildcards, one per line, matched against the relative path and the file name (*.txt, .git, app.exe ...)"},
	}
	if withOutput {
		items = append(items, widget.NewFormItem("Output GMA", a.pathEntry(f.output, true, nil, func(dir string) {
			name := slug(f.title.Text) + ".gma"
			f.output.SetText(filepath.Join(dir, name))
		})))
	}

	f.form = widget.NewForm(items...)
	return f
}

// folderChosen auto-loads addon.json when the form is still empty.
func (f *buildForm) folderChosen(dir string) {
	if strings.TrimSpace(f.title.Text) != "" {
		return
	}
	if p, err := gmaops.LoadAddonJSON(dir); err == nil {
		f.mergeProject(p)
	}
}

func parseRequired(text string) ([]string, error) {
	var out []string
	for _, field := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == ',' || r == ' ' || r == '\t' }) {
		id, err := workshop.ParseID(field)
		if err != nil {
			return nil, err
		}
		s := strconv.FormatUint(id, 10)
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out, nil
}

func splitLines(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func (f *buildForm) project() (*gmaops.Project, error) {
	v, err := strconv.Atoi(strings.TrimSpace(f.version.Text))
	if err != nil || v < 1 {
		return nil, errors.New("addon version must be a whole number ≥ 1")
	}

	req, err := parseRequired(f.required.Text)
	if err != nil {
		return nil, fmt.Errorf("required content: %w", err)
	}

	p := &gmaops.Project{
		Title:           strings.TrimSpace(f.title.Text),
		Description:     f.description.Text,
		Author:          strings.TrimSpace(f.author.Text),
		Version:         int32(v),
		Category:        f.category.Selected,
		Tags:            f.tags.selected(),
		RequiredContent: req,
		SourceDir:       strings.TrimSpace(f.sourceDir.Text),
		Ignore:          splitLines(f.ignore.Text),
		Output:          strings.TrimSpace(f.output.Text),
	}

	if p.Category == "" {
		return nil, errors.New("select the addon type")
	}
	return p, p.Validate()
}

// outputPath returns where the GMA should be written.
func (f *buildForm) outputPath(p *gmaops.Project) string {
	if !f.withOutput {
		q := *p
		q.Output = ""
		return f.a.svc.BuildOutput(&q)
	}
	return f.a.svc.BuildOutput(p)
}

func (f *buildForm) setProject(p *gmaops.Project) {
	f.title.SetText(p.Title)
	f.description.SetText(p.Description)
	if p.Author != "" {
		f.author.SetText(p.Author)
	}
	if p.Version > 0 {
		f.version.SetText(strconv.Itoa(int(p.Version)))
	}
	f.category.SetSelected(p.Category)
	f.tags.setSelected(p.Tags)
	f.required.SetText(strings.Join(p.RequiredContent, "\n"))
	f.sourceDir.SetText(p.SourceDir)
	if p.Ignore != nil {
		f.ignore.SetText(strings.Join(p.Ignore, "\n"))
	}
	f.output.SetText(p.Output)
}

// mergeProject fills fields from p without clearing values p does not have.
func (f *buildForm) mergeProject(p *gmaops.Project) {
	if p.Title != "" {
		f.title.SetText(p.Title)
	}
	if p.Description != "" {
		f.description.SetText(p.Description)
	}
	if p.Author != "" {
		f.author.SetText(p.Author)
	}
	if p.Version > 0 {
		f.version.SetText(strconv.Itoa(int(p.Version)))
	}
	if gma.CategoryExists(p.Category) {
		f.category.SetSelected(p.Category)
	}
	var tags []string
	for _, t := range p.Tags {
		if gma.TagExists(t) && len(tags) < 3 {
			tags = append(tags, t)
		}
	}
	if len(tags) > 0 {
		f.tags.setSelected(tags)
	}
	if len(p.RequiredContent) > 0 {
		f.required.SetText(strings.Join(p.RequiredContent, "\n"))
	}
	if len(p.Ignore) > 0 {
		merged := splitLines(f.ignore.Text)
		for _, i := range p.Ignore {
			if !slices.Contains(merged, i) {
				merged = append(merged, i)
			}
		}
		f.ignore.SetText(strings.Join(merged, "\n"))
	}
}

func (f *buildForm) loadAddonJSON() {
	dir := strings.TrimSpace(f.sourceDir.Text)
	if dir == "" {
		f.a.showError(errors.New("choose the addon folder first"))
		return
	}
	p, err := gmaops.LoadAddonJSON(dir)
	if err != nil {
		f.a.showError(err)
		return
	}
	f.mergeProject(p)
}

func (f *buildForm) saveAddonJSON() {
	p, err := f.project()
	if err != nil {
		f.a.showError(err)
		return
	}
	if err := gmaops.SaveAddonJSON(p); err != nil {
		f.a.showError(err)
		return
	}
	dialog.ShowInformation("Saved", "Wrote "+filepath.Join(p.SourceDir, "addon.json"), f.a.win)
}

// scan shows which files would be packed.
func (f *buildForm) scan() {
	p, err := f.project()
	if err != nil {
		f.a.showError(err)
		return
	}
	res, err := gmaops.Scan(p)
	if err != nil {
		f.a.showError(err)
		return
	}

	section := func(lines []string) fyne.CanvasObject {
		text := strings.Join(lines, "\n")
		if text == "" {
			text = "(none)"
		}
		l := widget.NewLabel(text)
		l.TextStyle.Monospace = true
		return l
	}

	included := make([]string, len(res.Included))
	for i, e := range res.Included {
		included[i] = fmt.Sprintf("%-10s %s", humanSize(e.Size), e.Internal)
	}

	acc := widget.NewAccordion(
		widget.NewAccordionItem(fmt.Sprintf("Included (%d, %s)", len(res.Included), humanSize(res.TotalSize)), section(included)),
		widget.NewAccordionItem(fmt.Sprintf("Ignored by wildcard (%d)", len(res.Ignored)), section(res.Ignored)),
		widget.NewAccordionItem(fmt.Sprintf("Not allowed in GMA, will be skipped (%d)", len(res.Disallowed)), section(res.Disallowed)),
	)
	acc.Open(0)
	if len(res.Disallowed) > 0 {
		acc.Open(2)
	}

	d := dialog.NewCustom("Files", "Close", container.NewVScroll(acc), f.a.win)
	d.Resize(f.a.dialogSize())
	d.Show()
}

func (f *buildForm) toolbar() fyne.CanvasObject {
	return container.NewHBox(
		widget.NewButtonWithIcon("Load addon.json", theme.FileIcon(), f.loadAddonJSON),
		widget.NewButtonWithIcon("Save addon.json", theme.DocumentSaveIcon(), f.saveAddonJSON),
		widget.NewButtonWithIcon("Preview files", theme.ListIcon(), f.scan),
	)
}

// buildTask builds the project inside a task.
func (a *App) buildTask(t *task, p *gmaops.Project, out string) error {
	_, err := a.svc.Build(p, out, t.reporter())
	return err
}

type buildPage struct {
	a    *App
	form *buildForm
}

func newBuildPage(a *App) *buildPage {
	return &buildPage{a: a, form: newBuildForm(a, true)}
}

func (b *buildPage) run() {
	p, err := b.form.project()
	if err != nil {
		b.a.showError(err)
		return
	}
	out := b.form.outputPath(p)

	b.a.runTask("Build GMA", func(t *task) error {
		return b.a.buildTask(t, p, out)
	}, func() {
		d := dialog.NewCustomConfirm("Build complete", "Upload…", "Close",
			wrapLabel("Created "+out), func(upload bool) {
				if upload {
					b.a.upload.useGMA(out, p)
					b.a.showWorkshopTab(1)
				}
			}, b.a.win)
		d.Show()
	})
}

func (b *buildPage) object() fyne.CanvasObject {
	buildBtn := widget.NewButtonWithIcon("Build GMA", theme.ConfirmIcon(), b.run)
	buildBtn.Importance = widget.HighImportance

	openBtn := widget.NewButtonWithIcon("Open output folder", theme.FolderIcon(), func() {
		dir := b.a.cfg.Builds()
		if o := strings.TrimSpace(b.form.output.Text); o != "" {
			dir = filepath.Dir(o)
		}
		os.MkdirAll(dir, 0o755)
		b.a.openPath(dir)
	})

	return container.NewBorder(
		b.form.toolbar(),
		container.NewHBox(buildBtn, openBtn),
		nil, nil,
		container.NewVScroll(b.form.form),
	)
}

// tagPicker is a grid of checkboxes allowing at most limit selections.
type tagPicker struct {
	names  []string
	checks []*widget.Check
	grid   *fyne.Container
}

func newTagPicker(names []string, limit int) *tagPicker {
	t := &tagPicker{names: names}
	objs := make([]fyne.CanvasObject, len(names))
	for i, n := range names {
		c := widget.NewCheck(n, nil)
		c.OnChanged = func(on bool) {
			if on && len(t.selected()) > limit {
				c.SetChecked(false)
			}
		}
		t.checks = append(t.checks, c)
		objs[i] = c
	}
	t.grid = container.NewGridWithColumns(5, objs...)
	return t
}

func (t *tagPicker) selected() []string {
	var out []string
	for i, c := range t.checks {
		if c.Checked {
			out = append(out, t.names[i])
		}
	}
	return out
}

func (t *tagPicker) setSelected(sel []string) {
	for i, c := range t.checks {
		c.SetChecked(slices.Contains(sel, t.names[i]))
	}
}
