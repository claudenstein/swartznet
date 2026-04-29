package companion_test

import (
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/companion"
)

func TestCompanionFileName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		pubkey string
		want   string
	}{
		{"empty pubkey falls back to legacy name", "", companion.FormatFileName},
		{"long pubkey truncates to 12-char prefix",
			"abcdef0123456789aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"swartznet-content-index-abcdef012345-v1.json.gz"},
		{"short pubkey is used verbatim",
			"deadbeef",
			"swartznet-content-index-deadbeef-v1.json.gz"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := companion.CompanionFileName(c.pubkey)
			if got != c.want {
				t.Errorf("CompanionFileName(%q) = %q, want %q", c.pubkey, got, c.want)
			}
			if !strings.HasSuffix(got, ".json.gz") {
				t.Errorf("CompanionFileName(%q) = %q, missing .json.gz suffix", c.pubkey, got)
			}
		})
	}
}
