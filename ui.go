package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

func loadUI(w fyne.Window) fyne.CanvasObject {
	tabs := container.NewAppTabs(
		container.NewTabItem(
			"Steam Workshop",
			widget.NewLabel("Placeholder 1"),
		),
		container.NewTabItem(
			"Dedicated Server",
			widget.NewLabel("Placeholder 2"),
		),
		container.NewTabItem(
			"Settings",
			widget.NewLabel("Placeholder 3"),
		),
	)

	return tabs
}
