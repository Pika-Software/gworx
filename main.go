package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
)

func main() {
	a := app.NewWithID("eu.p1ka.gworx")
	// a.SetIcon()

	w := a.NewWindow("GWorX - Garry's Mod Toolkit")

	w.SetContent(loadUI(w))
	w.Resize(fyne.NewSize(680, 560))

	w.ShowAndRun()
}
