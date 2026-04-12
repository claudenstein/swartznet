package gui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// swartzTheme is a dark theme matching the web UI's CSS variables.
type swartzTheme struct{}

var _ fyne.Theme = (*swartzTheme)(nil)

func (s *swartzTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return color.NRGBA{R: 0x0e, G: 0x11, B: 0x16, A: 0xff} // --bg
	case theme.ColorNameButton:
		return color.NRGBA{R: 0x21, G: 0x26, B: 0x2d, A: 0xff} // --surface-2
	case theme.ColorNameDisabledButton:
		return color.NRGBA{R: 0x16, G: 0x1b, B: 0x22, A: 0xff} // --surface
	case theme.ColorNameDisabled:
		return color.NRGBA{R: 0x8b, G: 0x94, B: 0x9e, A: 0xff} // --text-dim
	case theme.ColorNameForeground:
		return color.NRGBA{R: 0xc9, G: 0xd1, B: 0xd9, A: 0xff} // --text
	case theme.ColorNameHover:
		return color.NRGBA{R: 0x30, G: 0x36, B: 0x3d, A: 0xff} // --border
	case theme.ColorNameInputBackground:
		return color.NRGBA{R: 0x16, G: 0x1b, B: 0x22, A: 0xff} // --surface
	case theme.ColorNameInputBorder:
		return color.NRGBA{R: 0x30, G: 0x36, B: 0x3d, A: 0xff} // --border
	case theme.ColorNameMenuBackground:
		return color.NRGBA{R: 0x1b, G: 0x20, B: 0x28, A: 0xff} // --bg-elev
	case theme.ColorNameOverlayBackground:
		return color.NRGBA{R: 0x1b, G: 0x20, B: 0x28, A: 0xff} // --bg-elev
	case theme.ColorNamePlaceHolder:
		return color.NRGBA{R: 0x8b, G: 0x94, B: 0x9e, A: 0xff} // --text-dim
	case theme.ColorNamePressed:
		return color.NRGBA{R: 0x1f, G: 0x6f, B: 0xeb, A: 0xff} // --accent-2
	case theme.ColorNamePrimary:
		return color.NRGBA{R: 0x58, G: 0xa6, B: 0xff, A: 0xff} // --accent
	case theme.ColorNameScrollBar:
		return color.NRGBA{R: 0x30, G: 0x36, B: 0x3d, A: 0xff} // --border
	case theme.ColorNameSeparator:
		return color.NRGBA{R: 0x30, G: 0x36, B: 0x3d, A: 0xff} // --border
	case theme.ColorNameSuccess:
		return color.NRGBA{R: 0x3f, G: 0xb9, B: 0x50, A: 0xff} // --good
	case theme.ColorNameError:
		return color.NRGBA{R: 0xf8, G: 0x51, B: 0x49, A: 0xff} // --bad
	case theme.ColorNameWarning:
		return color.NRGBA{R: 0xd2, G: 0x99, B: 0x22, A: 0xff} // --warn
	default:
		return theme.DefaultTheme().Color(name, theme.VariantDark)
	}
}

func (s *swartzTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (s *swartzTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (s *swartzTheme) Size(name fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(name)
}
