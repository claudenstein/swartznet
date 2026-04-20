package testlab_test

import (
	"context"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/testlab"
)

// TestPointerGetterMissingKeyReturnsError covers the
// `!ok → return error` branch of pointerGetter.GetInfohashPointer.
// The companion-scenario tests exercise the happy path, but no
// existing test asks for a key that was never put — so the
// not-found branch was untested.
func TestPointerGetterMissingKeyReturnsError(t *testing.T) {
	t.Parallel()
	store := testlab.NewMemoryPointerStore()
	getter := store.Getter()

	var pub [32]byte
	pub[0] = 0xAB
	ih, err := getter.GetInfohashPointer(context.Background(), pub, []byte("never-put"))
	if err == nil {
		t.Errorf("expected error for missing key, got ih=%x", ih)
	}
	if !strings.Contains(err.Error(), "pointer not found") {
		t.Errorf("err = %q, want it to mention 'pointer not found'", err.Error())
	}
	// Returned infohash should be the zero value on error.
	var zero [20]byte
	if ih != zero {
		t.Errorf("ih = %x, want zero value on error", ih)
	}
}
