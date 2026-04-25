package gui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// TestSwartzThemeColorSwitch walks every named color in the
// switch + the default-fallback branch. The Color switch was
// at 25% because only a handful of case-arms get hit when a
// real Fyne app renders. Direct test exercises the full
// table.
func TestSwartzThemeColorSwitch(t *testing.T) {
	t.Parallel()
	s := &swartzTheme{}
	names := []fyne.ThemeColorName{
		theme.ColorNameBackground,
		theme.ColorNameButton,
		theme.ColorNameDisabledButton,
		theme.ColorNameDisabled,
		theme.ColorNameForeground,
		theme.ColorNameHover,
		theme.ColorNameInputBackground,
		theme.ColorNameInputBorder,
		theme.ColorNameMenuBackground,
		theme.ColorNameOverlayBackground,
		theme.ColorNamePlaceHolder,
		theme.ColorNamePressed,
		theme.ColorNamePrimary,
		theme.ColorNameScrollBar,
		theme.ColorNameSeparator,
		theme.ColorNameSuccess,
		theme.ColorNameError,
		theme.ColorNameWarning,
		fyne.ThemeColorName("unknown-name"), // hits the default arm
	}
	for _, n := range names {
		c := s.Color(n, theme.VariantDark)
		if c == nil {
			t.Errorf("Color(%q) returned nil", n)
		}
	}
}

// TestSwartzThemeFontIconSize covers the three pass-through
// methods. Each delegates to theme.DefaultTheme(), so we just
// verify they return non-nil values for representative inputs.
func TestSwartzThemeFontIconSize(t *testing.T) {
	t.Parallel()
	s := &swartzTheme{}
	if s.Font(fyne.TextStyle{}) == nil {
		t.Error("Font returned nil")
	}
	if s.Icon(theme.IconNameSearch) == nil {
		t.Error("Icon returned nil")
	}
	if s.Size(theme.SizeNamePadding) <= 0 {
		t.Error("Size returned non-positive")
	}
}
