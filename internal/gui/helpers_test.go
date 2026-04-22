package gui

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
)

// --- humanBytes ---

func TestHumanBytes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1 << 20, "1.0 MiB"},
		{3 * (1 << 20), "3.0 MiB"},
		{1 << 30, "1.0 GiB"},
		{5 * (1 << 30), "5.0 GiB"},
		{1 << 40, "1.0 TiB"},
		{1 << 50, "1.0 PiB"},
		{1 << 60, "1.0 EiB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestHumanBytesZettabyteSafe guards against a panic when the input
// exceeds 1 ZiB (2^70 bytes). The previous implementation indexed
// "KMGTPE"[exp] where exp could grow past 5, yielding a runtime
// index-out-of-range. After the bound-fix, values above EiB clamp
// to the EiB representation.
func TestHumanBytesZettabyteSafe(t *testing.T) {
	t.Parallel()
	// math.MaxInt64 ≈ 8 EiB; picking a number beyond 1 ZiB would
	// require int128 so we can't stress the full path in Go, but
	// math.MaxInt64 is the biggest positive value the function can
	// ever see in practice. Make sure it doesn't panic and produces
	// a reasonable EiB-scale output.
	const maxI64 int64 = 1<<63 - 1
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("humanBytes(%d) panicked: %v", maxI64, r)
		}
	}()
	got := humanBytes(maxI64)
	if !strings.HasSuffix(got, "EiB") {
		t.Errorf("humanBytes(MaxInt64) = %q, want suffix EiB", got)
	}
}

// --- rateStr ---

func TestRateStr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   int64
		want string
	}{
		{0, "—"},
		{-1, "—"},
		{1, "1 B/s"},
		{1024, "1.0 KiB/s"},
		{1 << 20, "1.0 MiB/s"},
	}
	for _, c := range cases {
		if got := rateStr(c.in); got != c.want {
			t.Errorf("rateStr(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- parseKiB / kibStr / limitDisplay ---

func TestParseKiB(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"", 0, false},
		{"   ", 0, false},
		{"0", 0, false},
		{"1024", 1024, false},
		{"  500  ", 500, false},
		{"-1", 0, true},
		{"abc", 0, true},
		{"1.5", 0, true}, // not a whole number
	}
	for _, c := range cases {
		got, err := parseKiB(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("parseKiB(%q) err=%v, wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if got != c.want {
			t.Errorf("parseKiB(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestKibStr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{-1, "0"},
		{1024, "1"},
		{2048, "2"},
		{1536, "1"}, // truncates fractional KiB
	}
	for _, c := range cases {
		if got := kibStr(c.in); got != c.want {
			t.Errorf("kibStr(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLimitDisplay(t *testing.T) {
	t.Parallel()
	if got := limitDisplay(0); got != "unlimited" {
		t.Errorf("limitDisplay(0) = %q, want \"unlimited\"", got)
	}
	if got := limitDisplay(500); got != "500" {
		t.Errorf("limitDisplay(500) = %q, want \"500\"", got)
	}
}

// --- small bool/string helpers ---

func TestBoolInt(t *testing.T) {
	t.Parallel()
	if boolInt(true) != 1 {
		t.Error("boolInt(true) != 1")
	}
	if boolInt(false) != 0 {
		t.Error("boolInt(false) != 0")
	}
}

func TestBoolStr(t *testing.T) {
	t.Parallel()
	if boolStr(true) != "enabled" {
		t.Error("boolStr(true) != enabled")
	}
	if boolStr(false) != "disabled" {
		t.Error("boolStr(false) != disabled")
	}
}

func TestPortStr(t *testing.T) {
	t.Parallel()
	if got := portStr(0); got != "OS-assigned" {
		t.Errorf("portStr(0) = %q, want OS-assigned", got)
	}
	if got := portStr(42069); got != "42069" {
		t.Errorf("portStr(42069) = %q, want 42069", got)
	}
}

// --- pieceLengthLabels / pieceLengthFromLabel ---

func TestPieceLengthLabelsRoundTrip(t *testing.T) {
	t.Parallel()
	labels := pieceLengthLabels()
	if len(labels) == 0 {
		t.Fatal("pieceLengthLabels returned empty slice")
	}
	// Every label must survive the reverse lookup. Note that
	// "Auto" maps to value 0 on purpose — zero means "let the
	// torrent-create path pick via metainfo.ChoosePieceLength".
	// So we accept value >= 0 but require a non-negative result
	// (a missing label would also return 0, so we additionally
	// check that a synthetic-unknown label truly fails).
	for _, l := range labels {
		v := pieceLengthFromLabel(l)
		if v < 0 {
			t.Errorf("pieceLengthFromLabel(%q) = %d, want >=0", l, v)
		}
	}
	// First label must be "Auto" → 0; later labels must be > 0.
	if labels[0] != "Auto" {
		t.Errorf("labels[0] = %q, want Auto", labels[0])
	}
	if v := pieceLengthFromLabel(labels[0]); v != 0 {
		t.Errorf("pieceLengthFromLabel(Auto) = %d, want 0", v)
	}
	for _, l := range labels[1:] {
		if v := pieceLengthFromLabel(l); v <= 0 {
			t.Errorf("pieceLengthFromLabel(%q) = %d, want >0 (non-auto preset)", l, v)
		}
	}
}

func TestPieceLengthFromLabelUnknown(t *testing.T) {
	t.Parallel()
	if got := pieceLengthFromLabel("xyzzy"); got != 0 {
		t.Errorf("pieceLengthFromLabel(unknown) = %d, want 0", got)
	}
}

// --- splitLines ---

func TestSplitLines(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   \n  \n", nil},
		{"a\nb\nc", []string{"a", "b", "c"}},
		{"  a  \nb\n\nc  ", []string{"a", "b", "c"}},
		{"only-one-line", []string{"only-one-line"}},
	}
	for _, c := range cases {
		got := splitLines(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitLines(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitLines(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

// --- torrentFilter ---

func TestTorrentFilterMatches(t *testing.T) {
	t.Parallel()
	f := &torrentFilter{}
	cases := []struct {
		path string
		want bool
	}{
		{"/tmp/foo.torrent", true},
		{"/tmp/foo.TORRENT", false}, // filter is case-sensitive on Extension()
		{"/tmp/foo.txt", false},
		{"/tmp/noext", false},
	}
	for _, c := range cases {
		u := storage.NewFileURI(c.path)
		if got := f.Matches(u); got != c.want {
			t.Errorf("Matches(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// --- swartzTheme colours ---

func TestSwartzThemeColours(t *testing.T) {
	t.Parallel()
	th := &swartzTheme{}

	// Spot-check a handful of named colours from the palette. We
	// don't exhaustively enumerate every theme name — we only care
	// that the overrides fire for the names we've explicitly set,
	// AND that an unknown name falls through to Fyne's default
	// dark palette rather than a zero-valued colour.
	cases := []struct {
		name fyne.ThemeColorName
		want color.NRGBA
	}{
		{theme.ColorNameBackground, color.NRGBA{0x0e, 0x11, 0x16, 0xff}},
		{theme.ColorNamePrimary, color.NRGBA{0x58, 0xa6, 0xff, 0xff}},
		{theme.ColorNameError, color.NRGBA{0xf8, 0x51, 0x49, 0xff}},
	}
	for _, c := range cases {
		got := th.Color(c.name, theme.VariantDark)
		nrgba, ok := got.(color.NRGBA)
		if !ok {
			t.Errorf("Color(%s): expected NRGBA, got %T", c.name, got)
			continue
		}
		if nrgba != c.want {
			t.Errorf("Color(%s) = %v, want %v", c.name, nrgba, c.want)
		}
	}

	// Unknown colour name must not panic and must return a
	// non-nil color.Color. We deliberately do NOT assert on the
	// alpha channel — the default Fyne theme returns a
	// zero-alpha color for names it doesn't recognise, and that
	// behaviour is Fyne's to keep or change. We just want to
	// verify our switch falls through cleanly.
	got := th.Color(fyne.ThemeColorName("definitely-not-a-real-name"), theme.VariantDark)
	if got == nil {
		t.Error("Color(unknown) returned nil — switch default branch broken")
	}
}

// --- validateMagnetURI ---

func TestValidateMagnetURI(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in          string
		wantReason  string
		wantInvalid bool
	}{
		{
			in:          "https://example.com/foo.torrent",
			wantInvalid: true,
			wantReason:  "magnet URI must start with \"magnet:?\" — did you paste a regular URL?",
		},
		{
			in:          "magnet:?dn=foo",
			wantInvalid: true,
			wantReason:  "magnet URI is missing the \"xt=urn:btih:\" infohash parameter",
		},
		{
			in:          "magnet:?xt=urn:btih:c4405d27af8462e3d5e03c30c542f66e170fe4f8",
			wantInvalid: false,
		},
		{
			in:          "magnet:?xt=urn:btih:9564c13e1f67f40ec14bf0a2e54a86dea69ccebd&dn=foo",
			wantInvalid: false,
		},
	}
	for _, c := range cases {
		got := validateMagnetURI(c.in)
		invalid := got != ""
		if invalid != c.wantInvalid {
			t.Errorf("validateMagnetURI(%q) invalid=%v, want %v (reason=%q)", c.in, invalid, c.wantInvalid, got)
			continue
		}
		if c.wantInvalid && got != c.wantReason {
			t.Errorf("validateMagnetURI(%q) reason=%q, want %q", c.in, got, c.wantReason)
		}
	}
}

// --- friendlyAddErr ---

func TestFriendlyAddErr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"engine: magnet has zero infohash (caller must provide ...)", "the magnet URI's infohash is all zeros — it needs a real 40-character btih value"},
		{"engine: parse magnet: cannot decode", "the magnet URI is malformed and couldn't be parsed"},
		{"engine: closed", "the engine is shutting down; try again after restart"},
		{"some brand new error", "some brand new error"},
	}
	for _, c := range cases {
		if got := friendlyAddErr(errString(c.in)); got != c.want {
			t.Errorf("friendlyAddErr(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := friendlyAddErr(nil); got != "unknown error" {
		t.Errorf("friendlyAddErr(nil) = %q, want \"unknown error\"", got)
	}
}

// errString is a tiny error type for table-driven tests.
type errString string

func (e errString) Error() string { return string(e) }

// --- copyableValue ---

// copyableValue returns a plain Label for the empty / placeholder
// cases (so we don't invite users to Copy nothing) and a container
// with both the Label AND a Copy button for real values. We can't
// render Fyne widgets without an app, but we CAN inspect the type
// of the returned CanvasObject to confirm the shape is correct.
func TestCopyableValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		value         string
		wantPlainOnly bool // true → bare Label; false → container with Copy
	}{
		{"", true},
		{"unknown", true},
		{"disabled", true},
		{"abc123", false},
		{"long-ed25519-pubkey-1234567890-64-chars-of-hex-abcdefabcdefabcdefabcdef", false},
	}
	for _, c := range cases {
		got := copyableValue(c.value)
		if got == nil {
			t.Errorf("copyableValue(%q) returned nil", c.value)
			continue
		}
		// Identify by runtime type. *widget.Label vs *fyne.Container
		// is the coarse distinction we care about.
		typeName := reflectTypeName(got)
		if c.wantPlainOnly {
			if typeName != "*widget.Label" {
				t.Errorf("copyableValue(%q): got %s, want *widget.Label (plain-only placeholder)", c.value, typeName)
			}
		} else {
			if typeName == "*widget.Label" {
				t.Errorf("copyableValue(%q): got bare Label, want container with Copy button", c.value)
			}
		}
	}
}

func reflectTypeName(v interface{}) string {
	// Stdlib-only type name so we don't pull reflect just for a
	// test helper.
	// fmt.Sprintf("%T", v) produces the same string as
	// reflect.TypeOf(v).String().
	type nilErr error
	if v == nil {
		var e nilErr
		return fmt.Sprintf("%T", e)
	}
	return fmt.Sprintf("%T", v)
}

// --- windowForObject ---

// windowForObject must tolerate nil / no-app cases without
// panicking. Callers (e.g. search's .win() one-liner) rely on it
// returning nil gracefully when the GUI hasn't been initialised
// (tests, startup races).
func TestWindowForObjectSafeOnNil(t *testing.T) {
	t.Parallel()
	// With no Fyne app bound in the test binary, CurrentApp()
	// returns nil (or the zero-value default); either way the
	// helper must return nil rather than panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("windowForObject panicked: %v", r)
		}
	}()
	if got := windowForObject(nil); got != nil {
		t.Errorf("windowForObject(nil) = %v, want nil", got)
	}
}

// --- theme.Font / Icon / Size fall through ---

func TestSwartzThemeFontIconSizeFallThrough(t *testing.T) {
	t.Parallel()
	th := &swartzTheme{}
	// None of these should panic, and none should return nil for
	// any of the basic names Fyne itself defines.
	if r := th.Font(fyne.TextStyle{Bold: true}); r == nil {
		t.Error("Font returned nil for bold")
	}
	if r := th.Icon(theme.IconNameConfirm); r == nil {
		t.Error("Icon returned nil for Confirm")
	}
	if s := th.Size(theme.SizeNamePadding); s <= 0 {
		t.Errorf("Size(Padding) = %v, want > 0", s)
	}
}
