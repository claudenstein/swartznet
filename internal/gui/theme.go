// Package gui is the native Fyne frontend. It is PURE PRESENTATION over the
// same internal/daemon.Daemon the CLI and web UI use: every action flows
// through the Daemon and its subsystems (engine, the shared searchmux.Mux via
// Daemon.Search, the shared Confirm/Flag path, companion). The GUI holds no
// independent lifecycle, reconciliation, or spam-signal logic.
package gui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// swartzTheme is a fixed dark theme mirroring the web UI's CSS variables (it
// ignores the requested variant). Only Color is overridden; Font/Icon/Size
// delegate to the default theme.
type swartzTheme struct{}

var _ fyne.Theme = swartzTheme{}

func rgb(hex uint32) color.Color {
	return color.NRGBA{R: uint8(hex >> 16), G: uint8(hex >> 8), B: uint8(hex), A: 0xff}
}

func (swartzTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return rgb(0x0e1116)
	case theme.ColorNameButton:
		return rgb(0x21262d)
	case theme.ColorNameDisabledButton:
		return rgb(0x161b22)
	case theme.ColorNameDisabled:
		return rgb(0x8b949e)
	case theme.ColorNameForeground:
		return rgb(0xc9d1d9)
	case theme.ColorNameHover:
		return rgb(0x30363d)
	case theme.ColorNameInputBackground:
		return rgb(0x161b22)
	case theme.ColorNameInputBorder:
		return rgb(0x30363d)
	case theme.ColorNameMenuBackground:
		return rgb(0x1b2028)
	case theme.ColorNameOverlayBackground:
		return rgb(0x1b2028)
	case theme.ColorNamePlaceHolder:
		return rgb(0x8b949e)
	case theme.ColorNamePressed:
		return rgb(0x1f6feb)
	case theme.ColorNamePrimary:
		return rgb(0x58a6ff)
	case theme.ColorNameScrollBar:
		return rgb(0x30363d)
	case theme.ColorNameSeparator:
		return rgb(0x30363d)
	case theme.ColorNameSuccess:
		return rgb(0x3fb950)
	case theme.ColorNameError:
		return rgb(0xf85149)
	case theme.ColorNameWarning:
		return rgb(0xd29922)
	default:
		return theme.DefaultTheme().Color(name, theme.VariantDark)
	}
}

func (swartzTheme) Font(s fyne.TextStyle) fyne.Resource       { return theme.DefaultTheme().Font(s) }
func (swartzTheme) Icon(n fyne.ThemeIconName) fyne.Resource   { return theme.DefaultTheme().Icon(n) }
func (swartzTheme) Size(n fyne.ThemeSizeName) float32         { return theme.DefaultTheme().Size(n) }
