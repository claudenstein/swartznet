package rendezvous

import (
	"sort"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

// fakeJoiner is an in-memory Joiner recording the joined set + community flags.
type fakeJoiner struct {
	joined    map[metainfo.Hash]bool
	community map[metainfo.Hash]bool
	adds      int
	removes   int
}

func newFakeJoiner() *fakeJoiner {
	return &fakeJoiner{joined: map[metainfo.Hash]bool{}, community: map[metainfo.Hash]bool{}}
}

func (f *fakeJoiner) AddRendezvous(h metainfo.Hash, community bool) error {
	if !f.joined[h] {
		f.joined[h] = true
		f.community[h] = community
		f.adds++
	}
	return nil
}
func (f *fakeJoiner) RemoveRendezvous(h metainfo.Hash) error {
	if f.joined[h] {
		delete(f.joined, h)
		f.removes++
	}
	return nil
}
func (f *fakeJoiner) RendezvousInfoHashes() []string {
	out := make([]string, 0, len(f.joined))
	for h := range f.joined {
		out = append(out, h.HexString())
	}
	sort.Strings(out)
	return out
}

func TestDesiredSwarms(t *testing.T) {
	cfg := Config{Global: true, Topics: []string{"linux", "linux", "", "  "}, Communities: []string{"secret", ""}}
	got := cfg.DesiredSwarms()
	// global + "linux" (deduped, empties skipped) + community("secret") = 3
	if len(got) != 3 {
		t.Fatalf("DesiredSwarms = %d entries, want 3: %v", len(got), got)
	}
	topic, _ := TopicInfoHash("linux")
	comm, _ := CommunityInfoHash("secret")
	if got[0].Hash != GlobalInfoHash() || got[1].Hash != topic || got[2].Hash != comm {
		t.Error("order not global,topic,community")
	}
	// Only the community swarm is tagged Community.
	if got[0].Community || got[1].Community || !got[2].Community {
		t.Errorf("community tagging wrong: %+v", got)
	}

	// Global off drops it.
	off := Config{Global: false, Topics: []string{"linux"}}
	if h := off.DesiredSwarms(); len(h) != 1 || h[0].Hash != topic || h[0].Community {
		t.Errorf("global-off DesiredSwarms = %v, want [public topic]", h)
	}
}

func TestReconcileJoinsAndLeaves(t *testing.T) {
	f := newFakeJoiner()
	m := NewManager(f, Config{Global: true, Topics: []string{"debian"}}, nil)

	m.Reconcile()
	if len(f.joined) != 2 || f.adds != 2 {
		t.Fatalf("after first reconcile: joined=%d adds=%d, want 2/2", len(f.joined), f.adds)
	}

	// Idempotent: a second reconcile with the same config changes nothing.
	m.Reconcile()
	if f.adds != 2 || f.removes != 0 {
		t.Errorf("second reconcile changed state: adds=%d removes=%d", f.adds, f.removes)
	}

	// Config narrows (drop the topic) → the topic swarm is left.
	topic, _ := TopicInfoHash("debian")
	m.cfg = Config{Global: true} // no topics
	m.Reconcile()
	if f.joined[topic] {
		t.Error("dropped topic still joined after reconcile")
	}
	if len(f.joined) != 1 || !f.joined[GlobalInfoHash()] {
		t.Errorf("after narrowing: joined=%v, want just global", f.RendezvousInfoHashes())
	}
}

// TestReconcileRemovesEverythingWhenEmpty: an all-off config leaves every swarm.
func TestReconcileRemovesEverythingWhenEmpty(t *testing.T) {
	f := newFakeJoiner()
	m := NewManager(f, Config{Global: true, Communities: []string{"team"}}, nil)
	m.Reconcile()
	if len(f.joined) != 2 {
		t.Fatalf("setup: joined=%d, want 2", len(f.joined))
	}
	m.cfg = Config{} // nothing desired
	m.Reconcile()
	if len(f.joined) != 0 {
		t.Errorf("after empty config: joined=%v, want none", f.RendezvousInfoHashes())
	}
}
