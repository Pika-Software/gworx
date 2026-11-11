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

	s.backend = widget.NewRadioGroup([]string{backendLabelSteamCMD, backendLabelSteamworks}, func(string) { s.updateCards() })
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
	s.steamcmdPath.SetPlaceHolder("Auto: " + orNone(workshop.FindSteamCMD()))
	s.username.SetText(cfg.SteamUsername)
	s.anonymous.SetChecked(cfg.AnonymousDownloads)
	s.installDir.SetText(cfg.SteamCMDInstallDir)
	s.installDir.SetPlaceHolder(filepath.Join(cfg.DataDir, "steamcmd"))

	s.steamAPIPath.SetText(cfg.SteamAPIPath)
	if found := workshop.FindSteamAPI(); len(found) > 0 {
		s.steamAPIPath.SetPlaceHolder("Auto: " + found[0])
	} else {
		s.steamAPIPath.SetPlaceHolder("Garry's Mod not found, select libsteam_api manually")
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
	s.steamID64.SetText(cfg.SteamID64)
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

	data := strings.TrimSpace(s.dataDir.Text)
	if data == "" {
		return errors.New("the data folder can't be empty")
	}
	cfg.DataDir = data
	cfg.DownloadsDir = strings.TrimSpace(s.downloadsDir.Text)
	cfg.UploadsDir = strings.TrimSpace(s.uploadsDir.Text)
	cfg.AutoExtract = s.autoExtract.Checked

	cfg.DefaultAuthor = strings.TrimSpace(s.author.Text)
	cfg.DefaultIgnore = splitLines(s.ignore.Text)

	cfg.WebAPIKey = strings.TrimSpace(s.webKey.Text)
	cfg.SteamID64 = strings.TrimSpace(s.steamID64.Text)

	if err := cfg.Save(); err != nil {
		return err
	}

	s.a.download.extract.SetChecked(cfg.AutoExtract)
	s.a.upload.refreshBackendLabel()
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
	if err := s.apply(); err != nil {
		s.a.showError(err)
		return
	}
	cmd := s.a.svc.SteamCMD()
	s.a.runTask("steamcmd login", func(t *task) error {
		t.log("Logging in as " + cmd.User + ". If Steam Guard uses the mobile app, confirm the login on your phone.")
		return cmd.Login(t.ctx, t.log)
	}, func() {
		dialog.ShowInformation("Logged in", "steamcmd cached your credentials. Uploads will reuse them without asking for the password.", s.a.win)
	})
}

func (s *settingsPage) testSteamworks() {
	if err := s.apply(); err != nil {
		s.a.showError(err)
		return
	}
	sw := s.a.svc.SW
	if !sw.Initialized() {
		sw.LibPath = s.a.cfg.SteamAPIPath
	}
	s.a.runTask("Connect to Steam", func(t *task) error {
		ctx, cancel := context.WithTimeout(t.ctx, time.Minute)
		defer cancel()
		items, err := sw.ListPublished(ctx, t.log)
		if err != nil {
			return err
		}
		t.log("Connected as " + sw.CurrentUser())
		t.log("Published Garry's Mod items: " + strconv.Itoa(len(items)))
		return nil
	}, func() {
		dialog.ShowInformation("Steamworks", "Connected to Steam as "+sw.CurrentUser()+".", s.a.win)
	})
}

func (s *settingsPage) detectSteamAPI() {
	found := workshop.FindSteamAPI()
	if len(found) == 0 {
		dialog.ShowInformation("Not found", "No Garry's Mod installation with a 64-bit steam_api library was found. On Linux, switch Garry's Mod to the x86-64 beta branch, or pick the library manually.", s.a.win)
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

func (s *settingsPage) object() fyne.CanvasObject {
	detectCmd := widget.NewButton("Detect", func() {
		if p := workshop.FindSteamCMD(); p != "" {
			s.steamcmdPath.SetText(p)
		} else {
			dialog.ShowInformation("Not found", "steamcmd was not found in PATH. Install it (https://developer.valvesoftware.com/wiki/SteamCMD) or select it manually.", s.a.win)
		}
	})
	loginBtn := widget.NewButtonWithIcon("Log in to Steam…", theme.LoginIcon(), s.login)

	s.steamcmdCard = widget.NewCard("steamcmd", "Credentials are cached by steamcmd after the first login; GWorX never stores your password.",
		widget.NewForm(
			widget.NewFormItem("steamcmd", container.NewBorder(nil, nil, nil, detectCmd, s.a.pathEntry(s.steamcmdPath, false, nil, nil))),
			widget.NewFormItem("Steam username", container.NewBorder(nil, nil, nil, loginBtn, s.username)),
			widget.NewFormItem("", s.anonymous),
			widget.NewFormItem("Install dir", s.a.pathEntry(s.installDir, true, nil, nil)),
		))

	detectAPI := widget.NewButton("Detect", s.detectSteamAPI)
	testBtn := widget.NewButtonWithIcon("Test connection", theme.ConfirmIcon(), s.testSteamworks)
	s.steamworksCard = widget.NewCard("Steamworks", "Uses the running Steam client. Steam shows you as playing Garry's Mod while GWorX is connected.",
		widget.NewForm(
			widget.NewFormItem("steam_api library", container.NewBorder(nil, nil, nil, detectAPI, s.a.pathEntry(s.steamAPIPath, false, nil, nil))),
			widget.NewFormItem("", container.NewHBox(testBtn)),
		))

	folders := widget.NewCard("Folders", "", widget.NewForm(
		widget.NewFormItem("Data folder", s.a.pathEntry(s.dataDir, true, nil, nil)),
		widget.NewFormItem("Downloaded GMAs", s.a.pathEntry(s.downloadsDir, true, nil, nil)),
		widget.NewFormItem("Uploads (extracted)", s.a.pathEntry(s.uploadsDir, true, nil, nil)),
		widget.NewFormItem("", s.autoExtract),
	))

	build := widget.NewCard("Build defaults", "", widget.NewForm(
		widget.NewFormItem("Default author", s.author),
		widget.NewFormItem("Default ignore list", s.ignore),
	))

	web := widget.NewCard("Steam Web API (optional)", "Lets \"My Addons → Refresh\" list your items when using steamcmd. Get a key at steamcommunity.com/dev/apikey.",
		widget.NewForm(
			widget.NewFormItem("API key", s.webKey),
			widget.NewFormItem("SteamID64", s.steamID64),
		))

	s.updateCards()

	saveBtn := widget.NewButtonWithIcon("Save", theme.DocumentSaveIcon(), s.save)
	saveBtn.Importance = widget.HighImportance
	revertBtn := widget.NewButtonWithIcon("Revert", theme.ContentUndoIcon(), s.load)

	body := container.NewVBox(
		widget.NewCard("Workshop backend", "", s.backend),
		s.steamcmdCard,
		s.steamworksCard,
		folders,
		build,
		web,
	)

	return container.NewBorder(nil, container.NewHBox(saveBtn, revertBtn), nil, nil, container.NewVScroll(body))
}
