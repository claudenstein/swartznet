package companion

import "testing"

// TestEncodeInteriorClearsFirstSeparator is the regression for the
// previously dead no-op block: EncodeInterior must force the first
// child's separator to empty (it is implicitly -∞), so a caller
// that passes a non-empty first separator can never produce a page
// whose first child carries a real lower bound.
func TestEncodeInteriorClearsFirstSeparator(t *testing.T) {
	children := []InteriorChild{
		{Separator: []byte("should-be-cleared"), ChildIndex: 1},
		{Separator: []byte("m"), ChildIndex: 2},
	}
	page, err := EncodeInterior(PageKindRoot, 0, children, MinPieceSize)
	if err != nil {
		t.Fatalf("EncodeInterior: %v", err)
	}
	_, decoded, err := DecodeInterior(page)
	if err != nil {
		t.Fatalf("DecodeInterior: %v", err)
	}
	if len(decoded) != 2 {
		t.Fatalf("decoded %d children, want 2", len(decoded))
	}
	if len(decoded[0].Separator) != 0 {
		t.Errorf("first child separator = %q, want empty", decoded[0].Separator)
	}
	// The non-first separator must survive verbatim.
	if string(decoded[1].Separator) != "m" {
		t.Errorf("second child separator = %q, want %q", decoded[1].Separator, "m")
	}
}
