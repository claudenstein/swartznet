package gui

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

//go:embed assets/Icon.png
var iconBytes []byte

// AppIcon is the embedded window/tray icon.
var AppIcon = fyne.NewStaticResource("Icon.png", iconBytes)
