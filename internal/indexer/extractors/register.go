package extractors

// init registers every shipped extractor in ONE explicit ordered list.
// Dispatch is first-claim-wins in registration order, so this list is
// wire-adjacent behavior: it must preserve the effective legacy order
// (init() file-order, i.e. alphabetical filenames):
//
//	archive, docx, epub, exif, fb2, flac, id3, mkv, mobi, mp4, odp,
//	odt, ogg, pdf, plaintext, pptx, rtf, subtitle, zim
//
// Slice 4 ships only the seven below; deferred extractors slot back
// into their alphabetical positions when they land. Among the shipped
// seven no claims overlap (plaintext explicitly declines the subtitle
// MIMEs), so dispatch outcomes are order-independent today — keep it
// that way or document the overlap here.
func init() {
	Register(NewDOCXExtractor(), claimsDOCX)
	Register(NewEPUBExtractor(), claimsEPUB)
	Register(NewODTExtractor(), claimsODT)
	Register(NewPDFExtractor(), claimsPDF)
	Register(NewPlaintextExtractor(), claimsPlaintext)
	Register(NewSubtitleExtractor(), claimsSubtitle)
	Register(NewZimExtractor(), claimsZIM)
}
