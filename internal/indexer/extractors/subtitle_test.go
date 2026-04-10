package extractors

import (
	"strings"
	"testing"
)

const sampleSRT = `1
00:00:01,000 --> 00:00:03,500
Hello, world.

2
00:00:04,000 --> 00:00:06,250
<i>This is subtitle dialog</i>
from chapter one.

3
00:00:06,500 --> 00:00:09,000
{\an8}<font color="red">The quick brown fox</font>
jumps over the lazy dog.
`

const sampleVTT = `WEBVTT
Kind: captions
Language: en

NOTE
This is a comment block
that should be ignored.

1
00:00:01.000 --> 00:00:03.500
Hello, world.

00:00:04.000 --> 00:00:06.250 line:90%
<v Speaker1>This is subtitle dialog</v>
from chapter one.
`

func TestSubtitleSRT(t *testing.T) {
	t.Parallel()
	e := NewSubtitleExtractor()
	chunks, err := e.Extract(strings.NewReader(sampleSRT), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d, want 1", len(chunks))
	}
	got := chunks[0].Text
	// All three cue bodies should be present.
	for _, want := range []string{
		"Hello, world.",
		"This is subtitle dialog",
		"from chapter one.",
		"The quick brown fox",
		"jumps over the lazy dog.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in extracted text:\n%s", want, got)
		}
	}
	// Timestamps and cue numbers must not leak in.
	for _, bad := range []string{
		"00:00:01,000",
		"00:00:04,000",
		"-->",
		"<i>",
		"<font",
		"{\\an8}",
	} {
		if strings.Contains(got, bad) {
			t.Errorf("unexpected noise %q in extracted text:\n%s", bad, got)
		}
	}
}

func TestSubtitleVTT(t *testing.T) {
	t.Parallel()
	e := NewSubtitleExtractor()
	chunks, err := e.Extract(strings.NewReader(sampleVTT), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatal("expected one chunk")
	}
	got := chunks[0].Text
	for _, want := range []string{
		"Hello, world.",
		"This is subtitle dialog",
		"from chapter one.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in extracted text:\n%s", want, got)
		}
	}
	// WEBVTT header, NOTE block, and timestamps must not leak in.
	for _, bad := range []string{
		"WEBVTT",
		"Kind: captions",
		"NOTE",
		"This is a comment block",
		"00:00:01.000",
		"line:90%",
		"<v Speaker1>",
	} {
		if strings.Contains(got, bad) {
			t.Errorf("unexpected noise %q in extracted text:\n%s", bad, got)
		}
	}
}

func TestSubtitleEmpty(t *testing.T) {
	t.Parallel()
	e := NewSubtitleExtractor()
	chunks, err := e.Extract(strings.NewReader(""), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 0 {
		t.Errorf("len(chunks) = %d, want 0", len(chunks))
	}
}

func TestSubtitleTimestampsOnly(t *testing.T) {
	t.Parallel()
	// A malformed SRT that has only timestamps and numbers should
	// produce an empty result, not leak noise.
	e := NewSubtitleExtractor()
	src := "1\n00:00:01,000 --> 00:00:03,000\n\n2\n00:00:03,000 --> 00:00:05,000\n\n"
	chunks, err := e.Extract(strings.NewReader(src), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected empty for pure-timecode input, got %q", chunks[0].Text)
	}
}

func TestSubtitleDispatchBeatsPlaintext(t *testing.T) {
	t.Parallel()
	// A .srt file must be claimed by the subtitle extractor, not
	// plaintext. This test protects the registration-order decoupling.
	e, mime := Dispatch(Candidate{Path: "episode.en.srt", Size: 16 * 1024})
	if e == nil {
		t.Fatal("no extractor dispatched for .srt file")
	}
	if e.Name() != "subtitle" {
		t.Errorf("extractor = %s (mime=%s), want subtitle", e.Name(), mime)
	}

	// .vtt should also go to subtitle.
	e, _ = Dispatch(Candidate{Path: "captions.vtt", Size: 8 * 1024})
	if e == nil || e.Name() != "subtitle" {
		name := "<nil>"
		if e != nil {
			name = e.Name()
		}
		t.Errorf("vtt dispatch = %s, want subtitle", name)
	}
}
