package admission

import "sync"

// State is the admission engine's bookkeeping: the configured anchor
// set, the admitted set (with the cap-exempt anchor subset), and the
// pending signals (endorsements and crawl observations that have not
// been admitted). Its accessors back the /aggregate probe and must
// distinguish a starved node (engine present, zero admitted) from a
// quiet network (no engine wired at all). Safe for concurrent use.
type State struct {
	mu sync.Mutex

	// anchorKeys is the configured trust-anchor set (channel A input),
	// validated at construction. AnchorCount reports its size — empty on
	// a fresh dev build with no operator anchors.
	anchorKeys [][32]byte

	admitted        map[[32]byte]struct{}
	anchorsAdmitted map[[32]byte]struct{}              // admitted anchors, exempt from the cap
	endorsements    map[[32]byte]map[[32]byte]struct{} // candidate -> endorsers
	observed        map[[32]byte]struct{}              // crawl-observed, not yet admitted
}

// newState builds an empty State bound to the validated anchor set.
func newState(anchorKeys [][32]byte) *State {
	return &State{
		anchorKeys:      anchorKeys,
		admitted:        make(map[[32]byte]struct{}),
		anchorsAdmitted: make(map[[32]byte]struct{}),
		endorsements:    make(map[[32]byte]map[[32]byte]struct{}),
		observed:        make(map[[32]byte]struct{}),
	}
}

// IsAdmitted reports whether the pubkey has been admitted.
func (s *State) IsAdmitted(pub [32]byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.admitted[pub]
	return ok
}

// IsPending reports whether the pubkey has been seen but not admitted:
// it has ANY endorsement, or was observed via a crawl, and is not
// admitted. An admitted pubkey is never pending.
func (s *State) IsPending(pub [32]byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.admitted[pub]; ok {
		return false
	}
	if _, ok := s.endorsements[pub]; ok {
		return true
	}
	_, ok := s.observed[pub]
	return ok
}

// AdmittedCount returns the total admitted publishers (anchors
// included).
func (s *State) AdmittedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.admitted)
}

// AnchorCount returns the number of configured trust-anchor pubkeys.
// Zero on a fresh dev build (DefaultAnchorPubkeys ships empty) — the
// "starved node" signal that /aggregate must render.
func (s *State) AnchorCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.anchorKeys)
}

// PendingCount returns the number of distinct pubkeys observed (via
// endorsements or crawl) but not yet admitted. A pubkey in both the
// endorsement and observed sets counts once.
func (s *State) PendingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := make(map[[32]byte]struct{}, len(s.endorsements)+len(s.observed))
	for k := range s.endorsements {
		if _, admitted := s.admitted[k]; admitted {
			continue
		}
		seen[k] = struct{}{}
	}
	for k := range s.observed {
		if _, admitted := s.admitted[k]; admitted {
			continue
		}
		seen[k] = struct{}{}
	}
	return len(seen)
}
