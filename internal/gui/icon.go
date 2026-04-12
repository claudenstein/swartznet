package gui

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

//go:embed assets/Icon.png
var iconBytes []byte

// AppIcon is the SwartzNet application icon used for the window
// title bar and system tray.
var AppIcon = fyne.NewStaticResource("Icon.png", iconBytes)
