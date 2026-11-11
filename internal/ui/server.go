package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Pika-Software/gworx/internal/core"
	"github.com/Pika-Software/gworx/internal/server"
)

// serverPage is the Dedicated Server tab: an instance picker and the page of
// the selected instance.
type serverPage struct {
	a *App

	picker *widget.Select
	ids    []string
	body   *fyne.Container
	view   *instanceView
}

func newServerPage(a *App) *serverPage {
	p := &serverPage{a: a, body: container.NewStack()}
	p.picker = widget.NewSelect(nil, func(string) { p.showSelected() })
	p.picker.PlaceHolder = "(no servers yet)"
	p.reload("")
	return p
}

func (p *serverPage) mgr() *server.Manager { return p.a.svc.Servers }

// reload refreshes the picker and selects id (or keeps the current selection).
func (p *serverPage) reload(id string) {
	if id == "" && p.picker.SelectedIndex() >= 0 && p.picker.SelectedIndex() < len(p.ids) {
		id = p.ids[p.picker.SelectedIndex()]
	}

	p.ids = nil
	var names []string
	sel := -1
	for i, in := range p.mgr().Instances() {
		p.ids = append(p.ids, in.ID)
		names = append(names, in.Name)
		if in.ID == id {
			sel = i
		}
	}
	if sel < 0 && len(p.ids) > 0 {
		sel = 0
	}

	p.picker.Options = names
	if sel >= 0 {
		p.picker.SetSelectedIndex(sel) // triggers showSelected
	} else {
		p.picker.ClearSelected()
		p.showSelected()
	}
	p.picker.Refresh()
}

func (p *serverPage) showSelected() {
	if p.view != nil {
		p.view.detach()
		p.view = nil
	}

	i := p.picker.SelectedIndex()
	if i < 0 || i >= len(p.ids) {
		p.body.Objects = []fyne.CanvasObject{container.NewCenter(container.NewVBox(
			widget.NewLabelWithStyle("No dedicated servers yet", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
			widget.NewLabelWithStyle("Create one; GWorX installs it with steamcmd.", fyne.TextAlignCenter, fyne.TextStyle{}),
			container.NewCenter(widget.NewButtonWithIcon("New server…", theme.ContentAddIcon(), p.create)),
		))}
		p.body.Refresh()
		return
	}

	in, ok := p.mgr().Get(p.ids[i])
	if !ok {
		return
	}
	p.view = newInstanceView(p, in)
	p.body.Objects = []fyne.CanvasObject{p.view.object()}
	p.body.Refresh()
}

// create asks for a name, location and branch, then offers to install.
func (p *serverPage) create() {
	cfg := p.a.cfg

	defaultDir := func(n string) string {
		if strings.TrimSpace(n) == "" {
			return filepath.Join(cfg.Servers(), "server")
		}
		return filepath.Join(cfg.Servers(), core.Slug(n))
	}

	name := widget.NewEntry()
	name.SetPlaceHolder("My Server")
	dir := widget.NewEntry()
	dir.SetText(defaultDir(""))
	dirEdited := false
	name.OnChanged = func(s string) {
		if !dirEdited {
			dir.SetText(defaultDir(s))
		}
	}
	dir.OnChanged = func(s string) {
		if s != defaultDir(name.Text) {
			dirEdited = true
		}
	}

	branch := widget.NewSelectEntry(server.Branches)
	branch.SetText("x86-64")

	items := []*widget.FormItem{
		widget.NewFormItem("Name", name),
		{Text: "Location", Widget: p.a.pathEntry(dir, true, nil, func(string) { dirEdited = true }),
			HintText: "An empty folder for a new install, or an existing server folder"},
		{Text: "Branch", Widget: branch, HintText: "x86-64 is the 64-bit server most servers use"},
	}

	d := dialog.NewForm("New dedicated server", "Create", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		in := server.NewInstance(strings.TrimSpace(name.Text), strings.TrimSpace(dir.Text))
		in.Branch = strings.TrimSpace(branch.Text)
		in, err := p.mgr().Create(in)
		if err != nil {
			p.a.showError(err)
			return
		}
		p.reload(in.ID)

		if in.Installed() {
			dialog.ShowInformation("Server added", "Found an existing server in "+in.Dir+". Press Start to run it, or Install / Update to update it.", p.a.win)
			return
		}
		dialog.ShowConfirm("Install server", fmt.Sprintf("Download Garry's Mod Dedicated Server (%s branch, a few GB) into\n%s now?", in.Branch, in.Dir), func(ok bool) {
			if ok && p.view != nil && p.view.id == in.ID {
				p.view.install(false)
			}
		}, p.a.win)
	}, p.a.win)
	d.Resize(fyne.NewSize(560, 0))
	d.Show()
	p.a.win.Canvas().Focus(name)
}

func (p *serverPage) object() fyne.CanvasObject {
	newBtn := widget.NewButtonWithIcon("New server…", theme.ContentAddIcon(), p.create)
	top := container.NewBorder(nil, nil, widget.NewLabel("Server"), newBtn, p.picker)

	var content fyne.CanvasObject = p.body
	if err := p.a.svc.ServersErr; err != nil {
		content = container.NewBorder(wrapLabel("Could not read servers.json: "+err.Error()), nil, nil, nil, p.body)
	}
	return container.NewBorder(container.NewVBox(top, widget.NewSeparator()), nil, nil, nil, content)
}

// instanceView is the page of one server instance.
type instanceView struct {
	p  *serverPage
	id string

	status   *widget.Label
	progress *widget.ProgressBar
	inf      *widget.ProgressBarInfinite

	startBtn, stopBtn, restartBtn, installBtn, deleteBtn *widget.Button
	validate                                             *widget.Check

	name, dir, extra           *widget.Entry
	branch, gamemode, level    *widget.SelectEntry
	maxPlayers, port, tickrate *widget.Entry
	developer                  *widget.Select
	command                    *widget.Label
	tokenNote                  *widget.Label
	saveBtn                    *widget.Button

	console *logView
	input   *widget.Entry
}

func newInstanceView(p *serverPage, in server.Instance) *instanceView {
	v := &instanceView{p: p, id: in.ID}
	r := p.mgr().Runner(in.ID)

	v.status = widget.NewLabel("")
	v.status.TextStyle.Bold = true
	v.progress = widget.NewProgressBar()
	v.progress.TextFormatter = func() string { return "" }
	v.progress.Hide()
	v.inf = widget.NewProgressBarInfinite()
	v.inf.Hide()
	v.inf.Stop()

	v.startBtn = widget.NewButtonWithIcon("Start", theme.MediaPlayIcon(), v.start)
	v.startBtn.Importance = widget.HighImportance
	v.stopBtn = widget.NewButtonWithIcon("Stop", theme.MediaStopIcon(), v.stop)
	v.restartBtn = widget.NewButtonWithIcon("Restart", theme.MediaReplayIcon(), v.restart)
	v.installBtn = widget.NewButtonWithIcon("Install / Update", theme.DownloadIcon(), func() { v.install(v.validate.Checked) })
	v.validate = widget.NewCheck("Validate files", nil)
	v.deleteBtn = widget.NewButtonWithIcon("", theme.DeleteIcon(), v.remove)

	// configuration form
	v.name = widget.NewEntry()
	v.dir = widget.NewEntry()
	v.branch = widget.NewSelectEntry(server.Branches)
	v.gamemode = widget.NewSelectEntry(server.Gamemodes)
	v.level = widget.NewSelectEntry(server.Maps)
	v.maxPlayers = numberEntry()
	v.port = numberEntry()
	v.tickrate = numberEntry()
	v.developer = widget.NewSelect([]string{"0", "1", "2"}, nil)
	v.extra = widget.NewEntry()
	v.extra.SetPlaceHolder("+host_workshop_collection 123456 -p2p ...")
	v.command = widget.NewLabel("")
	v.command.TextStyle.Monospace = true
	v.command.Wrapping = fyne.TextWrapBreak
	v.command.Selectable = true
	v.tokenNote = widget.NewLabel("")
	v.tokenNote.Wrapping = fyne.TextWrapWord
	v.tokenNote.Importance = widget.LowImportance
	v.saveBtn = widget.NewButtonWithIcon("Save", theme.DocumentSaveIcon(), func() { v.save() })

	v.load(in)
	for _, e := range []*widget.Entry{v.name, v.dir, v.maxPlayers, v.port, v.tickrate, v.extra} {
		e.OnChanged = func(string) { v.preview() }
	}
	for _, e := range []*widget.SelectEntry{v.branch, v.gamemode, v.level} {
		e.OnChanged = func(string) { v.preview() }
	}
	v.developer.OnChanged = func(string) { v.preview() }

	// console
	v.console = newLogViewWith(false)
	v.console.setLines(r.Lines())
	v.input = widget.NewEntry()
	v.input.SetPlaceHolder("Console command (status, changelevel gm_flatgrass, lua_run ...)")
	v.input.OnSubmitted = func(string) { v.send() }

	r.SetHandlers(
		func(line string) { v.console.add(line) },
		func(st server.State) { fyne.Do(func() { v.setState(st) }) },
	)
	v.setState(r.State())
	return v
}

func numberEntry() *widget.Entry {
	e := widget.NewEntry()
	e.Validator = func(s string) error {
		if _, err := strconv.Atoi(strings.TrimSpace(s)); err != nil {
			return errors.New("must be a number")
		}
		return nil
	}
	return e
}

// detach stops this view from receiving runner events.
func (v *instanceView) detach() {
	v.p.mgr().Runner(v.id).SetHandlers(nil, nil)
}

func (v *instanceView) load(in server.Instance) {
	v.name.SetText(in.Name)
	v.dir.SetText(in.Dir)
	v.branch.SetText(in.Branch)
	v.gamemode.SetText(in.Gamemode)
	v.level.SetText(in.Map)
	v.maxPlayers.SetText(strconv.Itoa(in.MaxPlayers))
	v.port.SetText(strconv.Itoa(in.Port))
	v.tickrate.SetText(strconv.Itoa(in.Tickrate))
	v.developer.SetSelected(strconv.Itoa(in.Developer))
	v.extra.SetText(in.ExtraArgs)
	v.preview()
}

// instance reads the form. Numbers that don't parse are left as 0 (caught by Validate).
func (v *instanceView) instance() server.Instance {
	in, _ := v.p.mgr().Get(v.id)
	atoi := func(e *widget.Entry) int {
		n, _ := strconv.Atoi(strings.TrimSpace(e.Text))
		return n
	}
	in.Name = strings.TrimSpace(v.name.Text)
	in.Dir = strings.TrimSpace(v.dir.Text)
	in.Branch = strings.TrimSpace(v.branch.Text)
	in.Gamemode = strings.TrimSpace(v.gamemode.Text)
	in.Map = strings.TrimSpace(v.level.Text)
	in.MaxPlayers = atoi(v.maxPlayers)
	in.Port = atoi(v.port)
	in.Tickrate = atoi(v.tickrate)
	in.Developer, _ = strconv.Atoi(v.developer.Selected)
	in.ExtraArgs = v.extra.Text
	return in
}

// preview shows the startup command for the current form values.
func (v *instanceView) preview() {
	if v.command == nil {
		return
	}
	cfg := v.p.a.cfg
	in := v.instance()
	if err := in.Validate(); err != nil {
		v.command.SetText("⚠ " + err.Error())
	} else {
		v.command.SetText(in.CommandLine(cfg.SteamAccountToken, cfg.ServerWrapper))
	}
	if cfg.SteamAccountToken == "" {
		v.tokenNote.SetText("No Steam Account Token set (Settings → Dedicated servers): the server won't be listed in the public server browser.")
	} else {
		v.tokenNote.SetText("The Steam Account Token from Settings is added as +sv_setsteamaccount.")
	}
}

func (v *instanceView) save() bool {
	in := v.instance()
	if err := v.p.mgr().Update(in); err != nil {
		v.p.a.showError(err)
		return false
	}
	v.p.reload(v.id)
	return true
}

// saveIfChanged stores pending form edits before starting.
func (v *instanceView) saveIfChanged() bool {
	stored, _ := v.p.mgr().Get(v.id)
	cur := v.instance()
	if stored == cur {
		return true
	}
	if err := v.p.mgr().Update(cur); err != nil {
		v.p.a.showError(err)
		return false
	}
	// keep the picker label in sync without rebuilding this view
	if stored.Name != cur.Name {
		for i, id := range v.p.ids {
			if id == v.id {
				v.p.picker.Options[i] = cur.Name
				v.p.picker.Refresh()
			}
		}
	}
	return true
}

func (v *instanceView) setState(st server.State) {
	v.status.SetText(st.String())
	switch st {
	case server.Running:
		v.status.Importance = widget.SuccessImportance
	case server.Installing, server.Starting, server.Stopping:
		v.status.Importance = widget.WarningImportance
	default:
		v.status.Importance = widget.MediumImportance
	}
	v.status.Refresh()

	enable := func(b *widget.Button, on bool) {
		if on {
			b.Enable()
		} else {
			b.Disable()
		}
	}
	enable(v.startBtn, st == server.Stopped)
	enable(v.installBtn, st == server.Stopped)
	enable(v.deleteBtn, st == server.Stopped)
	enable(v.stopBtn, st == server.Running || st == server.Installing)
	enable(v.restartBtn, st == server.Running)
	if st == server.Installing {
		v.stopBtn.SetText("Cancel")
	} else {
		v.stopBtn.SetText("Stop")
		v.progress.Hide()
		v.inf.Stop()
		v.inf.Hide()
	}
}

func (v *instanceView) run(fn func() error) {
	go func() {
		if err := fn(); err != nil && !errors.Is(err, context.Canceled) {
			fyne.Do(func() { v.p.a.showError(err) })
		}
	}()
}

func (v *instanceView) start() {
	if !v.saveIfChanged() {
		return
	}
	v.run(func() error { return v.p.mgr().Start(v.id) })
}

func (v *instanceView) stop() {
	v.run(func() error { return v.p.mgr().Runner(v.id).Stop() })
}

func (v *instanceView) restart() {
	if !v.saveIfChanged() {
		return
	}
	v.run(func() error { return v.p.mgr().Restart(v.id) })
}

func (v *instanceView) install(validate bool) {
	if !v.saveIfChanged() {
		return
	}
	v.inf.Show()
	v.inf.Start()
	v.run(func() error {
		return v.p.mgr().Install(context.Background(), v.id, validate, func(f float64) {
			fyne.Do(func() {
				if f < 0 {
					v.progress.Hide()
					v.inf.Show()
					v.inf.Start()
					return
				}
				v.inf.Stop()
				v.inf.Hide()
				v.progress.Show()
				v.progress.SetValue(f)
			})
		})
	})
}

func (v *instanceView) send() {
	cmd := strings.TrimSpace(v.input.Text)
	if cmd == "" {
		return
	}
	if err := v.p.mgr().Runner(v.id).Send(cmd); err != nil {
		v.p.a.showError(err)
		return
	}
	v.input.SetText("")
}

func (v *instanceView) openFolder() {
	dir := strings.TrimSpace(v.dir.Text)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		v.p.a.showError(err)
		return
	}
	v.p.a.openPath(dir)
}

func (v *instanceView) remove() {
	in, _ := v.p.mgr().Get(v.id)
	files := widget.NewCheck("Also delete the server files in "+in.Dir, nil)
	dialog.ShowCustomConfirm("Delete server", "Delete", "Cancel",
		container.NewVBox(widget.NewLabel(fmt.Sprintf("Remove %q from GWorX?", in.Name)), files),
		func(ok bool) {
			if !ok {
				return
			}
			if err := v.p.mgr().Delete(v.id, files.Checked); err != nil {
				v.p.a.showError(err)
			}
			v.p.reload("")
		}, v.p.a.win)
}

func (v *instanceView) object() fyne.CanvasObject {
	toolbar := container.NewHBox(
		v.startBtn, v.stopBtn, v.restartBtn,
		widget.NewSeparator(),
		v.installBtn, v.validate,
		widget.NewSeparator(),
		widget.NewButtonWithIcon("Open folder", theme.FolderOpenIcon(), v.openFolder),
	)
	header := container.NewBorder(nil, container.NewStack(v.progress, v.inf), nil,
		container.NewHBox(v.status, v.deleteBtn), container.NewHScroll(toolbar))

	form := widget.NewForm(
		widget.NewFormItem("Name", v.name),
		widget.NewFormItem("Location", v.p.a.pathEntry(v.dir, true, nil, nil)),
		&widget.FormItem{Text: "Branch", Widget: v.branch, HintText: "Install / Update after changing it"},
		widget.NewFormItem("Gamemode", v.gamemode),
		widget.NewFormItem("Level", v.level),
		widget.NewFormItem("Max players", v.maxPlayers),
		widget.NewFormItem("Port", v.port),
		widget.NewFormItem("Tickrate", v.tickrate),
		&widget.FormItem{Text: "developer", Widget: v.developer, HintText: "0 = off, 1/2 = more console output"},
		&widget.FormItem{Text: "Extra args", Widget: v.extra, HintText: "Added to the command line as-is"},
	)

	settings := container.NewVBox(
		form,
		container.NewHBox(v.saveBtn),
		widget.NewLabelWithStyle("Startup command", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		v.command,
		v.tokenNote,
	)

	sendBtn := widget.NewButtonWithIcon("Send", theme.MailSendIcon(), v.send)
	consoleTools := container.NewHBox(
		widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() { v.p.a.fa.Clipboard().SetContent(v.console.text()) }),
		widget.NewButtonWithIcon("", theme.ContentClearIcon(), func() {
			v.p.mgr().Runner(v.id).ClearConsole()
			v.console.clear()
		}),
	)
	console := container.NewBorder(
		widget.NewLabelWithStyle("Console", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewBorder(nil, nil, nil, container.NewHBox(sendBtn, consoleTools), v.input),
		nil, nil, v.console.panel(),
	)

	split := container.NewHSplit(container.NewVScroll(inset(settings)), insetSides(console))
	split.Offset = 0.42

	return container.NewBorder(header, nil, nil, nil, split)
}
