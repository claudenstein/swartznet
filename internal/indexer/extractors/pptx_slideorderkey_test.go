package extractors

import "testing"

// TestSlideOrderKeyAllBranches drives every branch of the
// slideOrderKey helper used to sort PPTX slide entries
// numerically: empty-after-trim, short numeric (padded), and
// long numeric (returned as-is).
func TestSlideOrderKeyAllBranches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		// Empty-after-trim branch — no numeric suffix.
		{"ppt/slides/slide.xml", ""},
		{"ppt/slides/slide", ""},
		// Short numeric → padded to 6 digits.
		{"ppt/slides/slide1.xml", "000001"},
		{"ppt/slides/slide2.xml", "000002"},
		{"ppt/slides/slide10.xml", "000010"},
		{"ppt/slides/slide99999.xml", "099999"},
		// Length 6+ → returned as-is (no padding needed).
		{"ppt/slides/slide123456.xml", "123456"},
		{"ppt/slides/slide9999999.xml", "9999999"},
	}
	for _, c := range cases {
		if got := slideOrderKey(c.in); got != c.want {
			t.Errorf("slideOrderKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
