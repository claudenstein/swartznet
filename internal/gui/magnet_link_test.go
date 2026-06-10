package gui

import "testing"

// TestMagnetLinkEscapesName is the regression test for magnet
// parameter injection: torrent names from search hits are
// remote/DHT-sourced, so an embedded '&' in the name used to
// inject extra magnet parameters (e.g. a hostile tracker) into
// both the clipboard magnet and the URI fed back into
// AddMagnetURI. The dn value must be URL-escaped; an empty name
// must omit dn entirely.
func TestMagnetLinkEscapesName(t *testing.T) {
	t.Parallel()
	const ih = "0123456789abcdef0123456789abcdef01234567"
	cases := []struct {
		name string
		dn   string
		want string
	}{
		{
			name: "empty name omits dn",
			dn:   "",
			want: "magnet:?xt=urn:btih:" + ih,
		},
		{
			name: "plain name",
			dn:   "ubuntu.iso",
			want: "magnet:?xt=urn:btih:" + ih + "&dn=ubuntu.iso",
		},
		{
			name: "ampersand cannot inject params",
			dn:   "foo&tr=udp://evil.example:6969",
			want: "magnet:?xt=urn:btih:" + ih + "&dn=foo%26tr%3Dudp%3A%2F%2Fevil.example%3A6969",
		},
		{
			name: "spaces and equals escaped",
			dn:   "my file=v2",
			want: "magnet:?xt=urn:btih:" + ih + "&dn=my+file%3Dv2",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := magnetLink(ih, c.dn); got != c.want {
				t.Errorf("magnetLink(%q) = %q, want %q", c.dn, got, c.want)
			}
		})
	}
}
