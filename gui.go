//go:build !nogui

package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"

	"github.com/Pika-Software/gworx/internal/config"
	"github.com/Pika-Software/gworx/internal/store"
	"github.com/Pika-Software/gworx/internal/ui"
)

var runGUI = func() error {
	a := app.NewWithID("eu.p1ka.gworx")
	a.Settings().SetTheme(ui.AppTheme())

	w := a.NewWindow("GWorX - Garry's Mod Toolkit")

	cfg, cfgErr := config.Load()
	st, stErr := store.Open(config.Dir())

	gui := ui.New(a, w, cfg, st)
	w.SetContent(gui.Content())
	w.Resize(fyne.NewSize(1000, 720))
	w.SetCloseIntercept(gui.RequestClose)

	for _, err := range []error{cfgErr, stErr} {
		if err != nil {
			dialog.ShowError(err, w)
		}
	}

	w.ShowAndRun()
	return nil
}
