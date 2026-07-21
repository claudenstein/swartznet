package rendezvous

import (
	"strings"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

// TestDeterministic: every derivation is stable across calls — independent nodes
// must compute byte-identical targets or they never meet.
func TestDeterministic(t *testing.T) {
	if GlobalInfoHash() != GlobalInfoHash() {
		t.Error("GlobalInfoHash not stable")
	}
	a, _ := TopicInfoHash("linux")
	b, _ := TopicInfoHash("linux")
	if a != b {
		t.Error("TopicInfoHash not stable")
	}
	c, _ := CommunityInfoHash("s3cret")
	d, _ := CommunityInfoHash("s3cret")
	if c != d {
		t.Error("CommunityInfoHash not stable")
	}
}

// TestDistinct: the three flavours and distinct inputs never collide.
func TestDistinct(t *testing.T) {
	g := GlobalInfoHash()
	linux, _ := TopicInfoHash("linux")
	debian, _ := TopicInfoHash("debian")
	comm, _ := CommunityInfoHash("linux") // same string, different scheme
	comm2, _ := CommunityInfoHash("debian")

	all := map[metainfo.Hash]string{
		g:      "global",
		linux:  "topic:linux",
		debian: "topic:debian",
		comm:   "community:linux",
		comm2:  "community:debian",
	}
	if len(all) != 5 {
		t.Fatalf("collision among the 5 distinct targets: got %d unique", len(all))
	}
	// A topic must not equal the global swarm even if someone picks "global".
	if gt, _ := TopicInfoHash("global"); gt == g {
		t.Error("topic 'global' collided with the global swarm")
	}
}

// TestTopicNormalization: spelling-equivalent topics map to the same swarm.
func TestTopicNormalization(t *testing.T) {
	want, ok := TopicInfoHash("ubuntu linux")
	if !ok {
		t.Fatal("valid topic reported not-ok")
	}
	for _, variant := range []string{
		"Ubuntu Linux",
		"  ubuntu   linux ",
		"UBUNTU\tlinux",
		"ubuntu\n\nlinux",
	} {
		got, ok := TopicInfoHash(variant)
		if !ok || got != want {
			t.Errorf("TopicInfoHash(%q) = %v ok=%v, want %v", variant, got, ok, want)
		}
	}
}

// TestEmptyInputsRejected: an empty/whitespace topic or empty secret is not a
// usable rendezvous point.
func TestEmptyInputsRejected(t *testing.T) {
	for _, empty := range []string{"", "   ", "\t\n"} {
		if h, ok := TopicInfoHash(empty); ok || h != (metainfo.Hash{}) {
			t.Errorf("TopicInfoHash(%q) = %v ok=%v, want zero+false", empty, h, ok)
		}
	}
	if h, ok := CommunityInfoHash(""); ok || h != (metainfo.Hash{}) {
		t.Errorf("CommunityInfoHash(\"\") = %v ok=%v, want zero+false", h, ok)
	}
}

// TestCommunityIsHMAC: the community target is a full 20-byte HMAC-SHA1 (never
// the zero hash for a non-empty secret) and different secrets diverge.
func TestCommunityIsHMAC(t *testing.T) {
	h, ok := CommunityInfoHash("team-alpha")
	if !ok {
		t.Fatal("non-empty secret reported not-ok")
	}
	if h == (metainfo.Hash{}) {
		t.Fatal("community hash is all zeros")
	}
	if other, _ := CommunityInfoHash("team-beta"); other == h {
		t.Error("distinct secrets produced the same community swarm")
	}
}

// TestNormalizeTopicTruncation: an over-long topic is cut to MaxTopicBytes on a
// rune boundary (never mid-character) so the derivation stays valid UTF-8.
func TestNormalizeTopicTruncation(t *testing.T) {
	long := strings.Repeat("é", MaxTopicBytes) // 2 bytes each → well over the cap
	got := NormalizeTopic(long)
	if len(got) > MaxTopicBytes {
		t.Errorf("normalized len = %d, want <= %d", len(got), MaxTopicBytes)
	}
	for i, r := range got {
		if r == '�' {
			t.Errorf("truncation split a rune at byte %d (produced U+FFFD)", i)
		}
	}
	// Still hashes fine (no panic, ok=true).
	if _, ok := TopicInfoHash(long); !ok {
		t.Error("truncated-but-nonempty topic reported not-ok")
	}
}
