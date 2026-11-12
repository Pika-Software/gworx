package ui

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Pika-Software/gworx/internal/config"
	"github.com/Pika-Software/gworx/internal/workshop"
)

const (
	backendLabelSteamCMD   = "steamcmd"
	backendLabelSteamworks = "Steamworks (Steam client)"
)

type settingsPage struct {
	a *App

	backend *widget.RadioGroup

	steamcmdPath *widget.Entry
	username     *widget.Entry
	anonymous    *widget.Check
	installDir   *widget.Entry

	steamAPIPath *widget.Entry

	dataDir      *widget.Entry
	downloadsDir *widget.Entry
	uploadsDir   *widget.Entry
	autoExtract  *widget.Check

	author *widget.Entry
	ignore *widget.Entry

	webKey    *widget.Entry
	steamID64 *widget.Entry

	serverToken   *widget.Entry
	serverWrapper *widget.Entry

	steamcmdCard   *widget.Card
	steamworksCard *widget.Card
}

func newSettingsPage(a *App) *settingsPage {
	s := &settingsPage{a: a}

	s.steamcmdPath = widget.NewEntry()
	s.username = widget.NewEntry()
	s.anonymous = widget.NewCheck("Download anonymously (no login needed for most Garry's Mod items)", nil)
	s.installDir = widget.NewEntry()
	s.steamAPIPath = widget.NewEntry()
	s.dataDir = widget.NewEntry()
	s.downloadsDir = widget.NewEntry()
	s.uploadsDir = widget.NewEntry()
	s.autoExtract = widget.NewCheck("Extract downloaded addons to the uploads folder by default", nil)
	s.author = widget.NewEntry()
	s.ignore = widget.NewMultiLineEntry()
	s.ignore.SetMinRowsVisible(5)
	s.webKey = widget.NewPasswordEntry()
	s.steamID64 = widget.NewEntry()
	s.serverToken = widget.NewPasswordEntry()
	s.serverWrapper = widget.NewEntry()
	s.serverWrapper.SetPlaceHolder("none (e.g. steam-run on NixOS)")

	s.backend = widget.NewRadioGroup([]string{backendLabelSteamworks, backendLabelSteamCMD}, func(string) { s.updateCards() })
	s.backend.Horizontal = true
	s.backend.Required = true

	s.load()
	return s
}

func (s *settingsPage) load() {
	cfg := s.a.cfg
	if cfg.Backend == config.BackendSteamworks {
		s.backend.SetSelected(backendLabelSteamworks)
	} else {
		s.backend.SetSelected(backendLabelSteamCMD)
	}

	s.steamcmdPath.SetText(cfg.SteamCMDPath)
	s.steamcmdPath.SetPlaceHolder("Auto: " + orNone(workshop.FindSteamCMD(filepath.Join(cfg.DataDir, "steamcmd"))))
	s.username.SetText(cfg.SteamUsername)
	s.anonymous.SetChecked(cfg.AnonymousDownloads)
	s.installDir.SetText(cfg.SteamCMDInstallDir)
	s.installDir.SetPlaceHolder(filepath.Join(cfg.DataDir, "steamcmd"))

	s.steamAPIPath.SetText(cfg.SteamAPIPath)
	if found := workshop.FindSteamAPI(); len(found) > 0 {
		s.steamAPIPath.SetPlaceHolder("Auto: " + found[0])
	} else {
		s.steamAPIPath.SetPlaceHolder("Not found: release builds ship it next to gworx; or select libsteam_api manually")
	}

	s.dataDir.SetText(cfg.DataDir)
	s.downloadsDir.SetText(cfg.DownloadsDir)
	s.downloadsDir.SetPlaceHolder(filepath.Join(cfg.DataDir, "downloads"))
	s.uploadsDir.SetText(cfg.UploadsDir)
	s.uploadsDir.SetPlaceHolder(filepath.Join(cfg.DataDir, "uploads"))
	s.autoExtract.SetChecked(cfg.AutoExtract)

	s.author.SetText(cfg.DefaultAuthor)
	s.ignore.SetText(strings.Join(cfg.DefaultIgnore, "\n"))

	s.webKey.SetText(cfg.WebAPIKey)
	s.serverToken.SetText(cfg.SteamAccountToken)
	s.serverWrapper.SetText(cfg.ServerWrapper)
	s.steamID64.SetText(cfg.SteamID64)

	s.updateCards()
}

func (s *settingsPage) revert() {
	if err := s.a.cfg.Reload(); err != nil {
		s.a.showError(err)
		return
	}
	s.load()
	s.a.download.extract.SetChecked(s.a.cfg.AutoExtract)
	s.a.upload.refreshBackendLabel()
	if v := s.a.servers.view; v != nil {
		v.preview()
	}
}

func orNone(s string) string {
	if s == "" {
		return "not found"
	}
	return s
}

func (s *settingsPage) updateCards() {
	if s.steamcmdCard == nil {
		return
	}
	if s.backend.Selected == backendLabelSteamworks {
		s.steamcmdCard.Hide()
		s.steamworksCard.Show()
	} else {
		s.steamworksCard.Hide()
		s.steamcmdCard.Show()
	}
}

func (s *settingsPage) apply() error {
	cfg := s.a.cfg

	// Validate before touching cfg so a failed save leaves it unchanged.
	data := strings.TrimSpace(s.dataDir.Text)
	if data == "" {
		return errors.New("the data folder can't be empty")
	}

	if s.backend.Selected == backendLabelSteamworks {
		cfg.Backend = config.BackendSteamworks
	} else {
		cfg.Backend = config.BackendSteamCMD
	}

	newLib := strings.TrimSpace(s.steamAPIPath.Text)
	if newLib != cfg.SteamAPIPath && s.a.svc.SW.Initialized() {
		dialog.ShowInformation("Restart required", "The Steam API is already loaded; the new library path is used after restarting GWorX.", s.a.win)
	}

	cfg.SteamCMDPath = strings.TrimSpace(s.steamcmdPath.Text)
	cfg.SteamUsername = strings.TrimSpace(s.username.Text)
	cfg.AnonymousDownloads = s.anonymous.Checked
	cfg.SteamCMDInstallDir = strings.TrimSpace(s.installDir.Text)
	cfg.SteamAPIPath = newLib

	cfg.DataDir = data
	cfg.DownloadsDir = strings.TrimSpace(s.downloadsDir.Text)
	cfg.UploadsDir = strings.TrimSpace(s.uploadsDir.Text)
	cfg.AutoExtract = s.autoExtract.Checked

	cfg.DefaultAuthor = strings.TrimSpace(s.author.Text)
	cfg.DefaultIgnore = splitLines(s.ignore.Text)

	cfg.WebAPIKey = strings.TrimSpace(s.webKey.Text)
	cfg.SteamAccountToken = strings.TrimSpace(s.serverToken.Text)
	cfg.ServerWrapper = strings.TrimSpace(s.serverWrapper.Text)
	cfg.SteamID64 = strings.TrimSpace(s.steamID64.Text)

	if err := cfg.Save(); err != nil {
		return err
	}

	s.a.download.extract.SetChecked(cfg.AutoExtract)
	s.a.upload.refreshBackendLabel()
	if v := s.a.servers.view; v != nil {
		v.preview()
	}
	return nil
}

func (s *settingsPage) save() {
	if err := s.apply(); err != nil {
		s.a.showError(err)
		return
	}
	s.load()
	dialog.ShowInformation("Settings", "Saved.", s.a.win)
}

func (s *settingsPage) login() {
	user := strings.TrimSpace(s.username.Text)
	if user == "" {
		s.a.showError(errors.New("set your Steam username first"))
		return
	}
	cmd := &workshop.SteamCMD{
		Path:       strings.TrimSpace(s.steamcmdPath.Text),
		User:       user,
		InstallDir: s.a.cfg.SteamCMDDir(),
		Wrapper:    strings.TrimSpace(s.serverWrapper.Text),
		Prompt:     s.a.svc.Prompt,
	}
	s.a.runTask("steamcmd login", func(t *task) error {
		t.log("Logging in as " + cmd.User + ". If Steam Guard uses the mobile app, confirm the login on your phone.")
		return cmd.Login(t.ctx, t.log)
	}, func() {
		dialog.ShowInformation("Logged in", "steamcmd cached your credentials. Uploads will reuse them without asking for the password.", s.a.win)
	})
}

func (s *settingsPage) testSteamworks() {
	sw := s.a.svc.SW
	sw.SetLibPath(strings.TrimSpace(s.steamAPIPath.Text))
	s.a.runTask("Connect to Steam", func(t *task) error {
		ctx, cancel := context.WithTimeout(t.ctx, time.Minute)
		defer cancel()
		items, err := sw.ListPublished(ctx, t.log)
		if err != nil {
			return err
		}
		t.log("Connected as " + sw.CurrentUser() + " using " + sw.Library())
		t.log("Published Garry's Mod items: " + strconv.Itoa(len(items)))
		return nil
	}, func() {
		dialog.ShowInformation("Steamworks", "Connected to Steam as "+sw.CurrentUser()+".\nLibrary: "+sw.Library(), s.a.win)
	})
}

func (s *settingsPage) detectSteamAPI() {
	found := workshop.FindSteamAPI()
	if len(found) == 0 {
		dialog.ShowInformation("Not found", "No steam_api library was found. Release builds ship it next to the gworx binary; Garry's Mod also has one (bin/win64, or bin/linux64 on the x86-64 branch). You can also pick it manually.", s.a.win)
		return
	}
	if len(found) == 1 {
		s.steamAPIPath.SetText(found[0])
		return
	}
	sel := widget.NewSelect(found, nil)
	sel.SetSelectedIndex(0)
	dialog.ShowCustomConfirm("Select library", "Use", "Cancel", sel, func(ok bool) {
		if ok {
			s.steamAPIPath.SetText(sel.Selected)
		}
	}, s.a.win)
}

func (s *settingsPage) downloadSteamCMD() {
	data := strings.TrimSpace(s.dataDir.Text)
	if data == "" {
		data = s.a.cfg.DataDir
	}
	dir := filepath.Join(data, "steamcmd")
	wrapper := strings.TrimSpace(s.serverWrapper.Text)
	var exe string
	s.a.runTask("Download steamcmd", func(t *task) error {
		var err error
		exe, err = workshop.InstallSteamCMD(t.ctx, dir, wrapper, t.log)
		return err
	}, func() {
		s.steamcmdPath.SetText(exe)
		dialog.ShowInformation("steamcmd", "Installed to "+exe+". Click Save to keep this path. Workshop downloads use the Install dir below (defaults to the same folder).", s.a.win)
	})
}

func (s *settingsPage) object() fyne.CanvasObject {
	detectCmd := widget.NewButton("Detect", func() {
		if p := workshop.FindSteamCMD(filepath.Join(s.a.cfg.DataDir, "steamcmd")); p != "" {
			s.steamcmdPath.SetText(p)
		} else {
			dialog.ShowInformation("Not found", "steamcmd was not found. Click Download to install it into the GWorX data folder, or install it yourself (https://developer.valvesoftware.com/wiki/SteamCMD).", s.a.win)
		}
	})
	downloadCmd := widget.NewButtonWithIcon("Download", theme.DownloadIcon(), s.downloadSteamCMD)
	cmdButtons := container.NewHBox(detectCmd, downloadCmd)
	loginBtn := widget.NewButtonWithIcon("Log in to Steam…", theme.LoginIcon(), s.login)

	s.steamcmdCard = card("steamcmd", "Credentials are cached by steamcmd after the first login; GWorX never stores your password. Download installs Valve's steamcmd into the data folder.",
		framePad(widget.NewForm(
			widget.NewFormItem("steamcmd", container.NewBorder(nil, nil, nil, cmdButtons, s.a.pathEntry(s.steamcmdPath, false, nil, nil))),
			widget.NewFormItem("Steam username", container.NewBorder(nil, nil, nil, loginBtn, s.username)),
			widget.NewFormItem("", s.anonymous),
			widget.NewFormItem("Install dir", s.a.pathEntry(s.installDir, true, nil, nil)),
		)))

	detectAPI := widget.NewButton("Detect", s.detectSteamAPI)
	testBtn := widget.NewButtonWithIcon("Test connection", theme.ConfirmIcon(), s.testSteamworks)
	s.steamworksCard = card("Steamworks", "Talks to the running Steam client using the bundled Steam API. Leave the path empty to use the bundled library. While connected, Steam shows you in Garry's Mod (\"In the Workshop\").",
		framePad(widget.NewForm(
			widget.NewFormItem("steam_api library", container.NewBorder(nil, nil, nil, detectAPI, s.a.pathEntry(s.steamAPIPath, false, nil, nil))),
			widget.NewFormItem("", container.NewHBox(testBtn)),
		)))

	folders := card("Folders", "", framePad(widget.NewForm(
		widget.NewFormItem("Data folder", s.a.pathEntry(s.dataDir, true, nil, nil)),
		widget.NewFormItem("Downloaded GMAs", s.a.pathEntry(s.downloadsDir, true, nil, nil)),
		widget.NewFormItem("Uploads (extracted)", s.a.pathEntry(s.uploadsDir, true, nil, nil)),
		widget.NewFormItem("", s.autoExtract),
	)))

	build := card("Build defaults", "", framePad(widget.NewForm(
		widget.NewFormItem("Default author", s.author),
		widget.NewFormItem("Default ignore list", s.ignore),
	)))

	servers := card("Dedicated servers", "Used by every server instance.", framePad(widget.NewForm(
		&widget.FormItem{Text: "Steam Account Token", Widget: s.serverToken,
			HintText: "Added as +sv_setsteamaccount. Create one at steamcommunity.com/dev/managegameservers (app 4000)"},
		&widget.FormItem{Text: "Launch wrapper", Widget: s.serverWrapper,
			HintText: "Command put in front of srcds_run and steamcmd (e.g. steam-run on NixOS)"},
	)))

	web := card("Steam Web API (optional)", "Lets \"My Addons → Refresh\" list your items when using steamcmd. Get a key at steamcommunity.com/dev/apikey.",
		framePad(widget.NewForm(
			widget.NewFormItem("API key", s.webKey),
			widget.NewFormItem("SteamID64", s.steamID64),
		)))

	s.updateCards()

	saveBtn := widget.NewButtonWithIcon("Save", theme.DocumentSaveIcon(), s.save)
	saveBtn.Importance = widget.HighImportance
	revertBtn := widget.NewButtonWithIcon("Revert", theme.ContentUndoIcon(), func() { s.revert() })

	body := container.NewVBox(
		card("Workshop Backend", "", framePad(s.backend)),
		s.steamcmdCard,
		s.steamworksCard,
		folders,
		build,
		servers,
		web,
	)

	return container.NewBorder(nil, container.NewHBox(saveBtn, revertBtn), nil, nil, container.NewVScroll(body))
}
