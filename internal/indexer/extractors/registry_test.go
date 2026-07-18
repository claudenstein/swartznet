package extractors

import "testing"

// TestExtractorNamesPinned freezes the Name() strings — they land
// verbatim on ContentDoc.Extractor and must survive refactors.
func TestExtractorNamesPinned(t *testing.T) {
	t.Parallel()
	cases := []struct {
		e    Extractor
		want string
	}{
		{NewDOCXExtractor(), "docx"},
		{NewEPUBExtractor(), "epub"},
		{NewODTExtractor(), "odt"},
		{NewPDFExtractor(), "pdf"},
		{NewPlaintextExtractor(), "plaintext"},
		{NewSubtitleExtractor(), "subtitle"},
		{NewZimExtractor(), "zim"},
	}
	for _, tc := range cases {
		if got := tc.e.Name(); got != tc.want {
			t.Errorf("Name() = %q, want %q", got, tc.want)
		}
	}
}

// TestDispatchDeferredTypesReturnNilWithMime pins the deferral
// contract: file types whose extractors are not yet ported dispatch
// to nil, but the resolved MIME still comes back so the pipeline can
// persist it (and log pipeline.no_extractor).
func TestDispatchDeferredTypesReturnNilWithMime(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path     string
		wantMime string
	}{
		{"song.mp3", "audio/mpeg"},
		{"slides.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
		{"deck.odp", "application/vnd.oasis.opendocument.presentation"},
		{"book.mobi", "application/x-mobipocket-ebook"},
		{"book.fb2", "application/x-fictionbook+xml"},
		{"doc.rtf", "application/rtf"},
		{"video.mp4", "video/mp4"},
		{"video.mkv", "video/x-matroska"},
		{"track.flac", "audio/flac"},
		{"track.ogg", "audio/ogg"},
		{"backup.tar", "application/x-tar"},
		{"photo.jpg", "image/jpeg"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			e, mime := Dispatch(Candidate{Path: tc.path, Size: 1024})
			if e != nil {
				t.Errorf("Dispatch(%s) = %q, want nil (extractor deferred)", tc.path, e.Name())
			}
			if mime != tc.wantMime {
				t.Errorf("Dispatch(%s) mime = %q, want %q", tc.path, mime, tc.wantMime)
			}
		})
	}
}

// TestDispatchNoClaimUnknownType pins the nobody-claims path for a
// genuinely unknown extension: nil extractor AND empty mime.
func TestDispatchNoClaimUnknownType(t *testing.T) {
	t.Parallel()
	e, mime := Dispatch(Candidate{Path: "data.bin-nosuch", Size: 64})
	if e != nil {
		t.Errorf("Dispatch(unknown) = %q, want nil", e.Name())
	}
	if mime != "" {
		t.Errorf("Dispatch(unknown) mime = %q, want empty", mime)
	}
}
