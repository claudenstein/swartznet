package companion

import (
	"strings"
	"testing"
)

// TestVerifyFingerprintBailsEarlyOnExcessRecords is the regression
// for the early-bail guard: when the leaves hold more records than
// the trailer claims, VerifyFingerprint must stop as soon as the
// running count exceeds trailer.NumRecords rather than hashing the
// entire (possibly attacker-inflated) record stream first.
func TestVerifyFingerprintBailsEarlyOnExcessRecords(t *testing.T) {
	r, _, _, _ := buildTestTree(t, 30, []string{"alpha", "beta", "gamma"}, MinPieceSize)

	// Honest tree first: VerifyFingerprint must succeed.
	if err := r.VerifyFingerprint(); err != nil {
		t.Fatalf("honest VerifyFingerprint failed: %v", err)
	}

	// Forge the trailer to claim fewer records than the leaves
	// actually hold. The signature is not re-checked by
	// VerifyFingerprint, so this models a tree whose trailer
	// under-declares NumRecords.
	if r.trailer.NumRecords == 0 {
		t.Fatal("test setup: expected non-zero NumRecords")
	}
	r.trailer.NumRecords = 1

	err := r.VerifyFingerprint()
	if err == nil {
		t.Fatal("VerifyFingerprint should reject a count exceeding the trailer claim")
	}
	if !strings.Contains(err.Error(), "trailer claim exceeded") {
		t.Errorf("error %q does not indicate the early-bail path", err.Error())
	}
}
