package extractors

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// ZimExtractor reads OpenZIM files (the format Kiwix ships for offline
// Wikipedia / Project Gutenberg / Stack Exchange / etc.) and emits one
// extracted-text chunk per indexable article.
//
// ZIM is a single-file binary container holding tens of thousands of
// HTML / plaintext articles compressed in clusters. The extractor:
//
//  1. Reads the 80-byte header.
//  2. Walks the MIME-type list, the URL pointer list, and the cluster
//     pointer list.
//  3. For each article whose MIME is text-like (text/html, text/plain,
//     application/xhtml+xml), reads the cluster, decompresses if
//     needed, slices out the blob, and decodes HTML→text via the
//     existing extractHTMLText helper.
//
// Bounds: the extractor processes at most maxArticles entries and emits
// at most maxBytes of cumulative text (defaults: 5000 articles, 32 MiB
// text). These caps keep the extractor useful on a 70 GiB ZIM without
// blowing through memory or runtime.
//
// Cluster compression types supported in v1:
//   - 1 (uncompressed)
//   - 5 (zstd, what every Kiwix release since 2021 uses)
//
// Type 4 (XZ / LZMA2) is detected and skipped with a debug-level
// reason; old pre-2021 ZIMs will index zero articles. Type 4 support
// would add a third compression dep and is deferred.
type ZimExtractor struct{}

// NewZimExtractor returns a ready-to-use ZIM extractor.
func NewZimExtractor() *ZimExtractor { return &ZimExtractor{} }

// Name implements Extractor.
func (*ZimExtractor) Name() string { return "zim" }

// ZIM constants from <https://wiki.openzim.org/wiki/ZIM_file_format>.
const (
	zimMagic            uint32 = 0x44D495A // little-endian "ZIM\x04"
	zimRedirectMime     uint16 = 0xFFFF
	zimLinkTargetMime   uint16 = 0xFFFE
	zimDeletedMime      uint16 = 0xFFFD
	zimCompUncompressed uint8  = 1
	zimCompXZ           uint8  = 4 // not supported in v1
	zimCompZstd         uint8  = 5

	zimDefaultMaxArticles = 5000
	zimDefaultMaxBytes    = 32 * 1024 * 1024
	zimMaxClusterBytes    = 64 * 1024 * 1024 // hard cap per cluster
	zimMaxMimeListBytes   = 64 * 1024
)

// Extract implements Extractor. The reader MUST also implement
// io.ReaderAt — otherwise random access into the (potentially huge)
// ZIM file is impossible and the extractor returns an error. The
// engine's anacrolix file reader satisfies this.
func (*ZimExtractor) Extract(r io.Reader, maxBytes int64) ([]Chunk, error) {
	ra, ok := r.(io.ReaderAt)
	if !ok {
		return nil, errors.New("zim: extractor requires io.ReaderAt")
	}
	if maxBytes <= 0 {
		maxBytes = zimDefaultMaxBytes
	}

	hdr, err := readZimHeader(ra)
	if err != nil {
		return nil, err
	}
	mimes, err := readZimMimeList(ra, int64(hdr.MimeListPos))
	if err != nil {
		return nil, err
	}

	articleCount := hdr.ArticleCount
	if articleCount > zimDefaultMaxArticles {
		articleCount = zimDefaultMaxArticles
	}

	// Keyed cache of decompressed clusters so multiple articles
	// sharing one cluster only pay decompression once. Bounded by
	// count, evicting half the entries when full — random eviction,
	// not LRU, since the URL pointer list is roughly cluster-sorted
	// in practice and adjacent articles tend to live in the same
	// cluster.
	clusterCache := make(map[uint32][]byte)
	const cacheCap = 32

	var (
		chunks  []Chunk
		emitted int64
	)

	for i := uint32(0); i < articleCount; i++ {
		if emitted >= maxBytes {
			break
		}
		entry, err := readZimDirEntry(ra, hdr, i)
		if err != nil {
			continue
		}
		if entry.IsRedirect || entry.IsDeleted {
			continue
		}
		if int(entry.MimeIdx) >= len(mimes) {
			continue
		}
		if !zimIsExtractableMime(mimes[entry.MimeIdx]) {
			continue
		}

		cluster, ok := clusterCache[entry.ClusterNum]
		if !ok {
			cluster, err = readZimCluster(ra, hdr, entry.ClusterNum)
			if err != nil {
				continue
			}
			if len(clusterCache) >= cacheCap {
				dropped := 0
				for k := range clusterCache {
					delete(clusterCache, k)
					dropped++
					if dropped >= cacheCap/2 {
						break
					}
				}
			}
			clusterCache[entry.ClusterNum] = cluster
		}

		blob, err := getZimBlob(cluster, entry.BlobNum)
		if err != nil {
			continue
		}
		text := zimDecodeArticle(blob, mimes[entry.MimeIdx])
		if text == "" {
			continue
		}
		chunks = append(chunks, Chunk{Text: text})
		emitted += int64(len(text))
	}
	return chunks, nil
}

// zimHeader is the parsed 80-byte file header.
type zimHeader struct {
	MagicNumber   uint32
	MajorVersion  uint16
	MinorVersion  uint16
	UUID          [16]byte
	ArticleCount  uint32
	ClusterCount  uint32
	URLPtrPos     uint64
	TitlePtrPos   uint64
	ClusterPtrPos uint64
	MimeListPos   uint64
	MainPage      uint32
	LayoutPage    uint32
	ChecksumPos   uint64
}

// readZimHeader reads + parses the fixed-size header.
func readZimHeader(ra io.ReaderAt) (*zimHeader, error) {
	var buf [80]byte
	if _, err := ra.ReadAt(buf[:], 0); err != nil {
		return nil, fmt.Errorf("zim: read header: %w", err)
	}
	h := &zimHeader{
		MagicNumber:  binary.LittleEndian.Uint32(buf[0:4]),
		MajorVersion: binary.LittleEndian.Uint16(buf[4:6]),
		MinorVersion: binary.LittleEndian.Uint16(buf[6:8]),
	}
	if h.MagicNumber != zimMagic {
		return nil, fmt.Errorf("zim: bad magic 0x%08X (want 0x%08X)", h.MagicNumber, zimMagic)
	}
	copy(h.UUID[:], buf[8:24])
	h.ArticleCount = binary.LittleEndian.Uint32(buf[24:28])
	h.ClusterCount = binary.LittleEndian.Uint32(buf[28:32])
	h.URLPtrPos = binary.LittleEndian.Uint64(buf[32:40])
	h.TitlePtrPos = binary.LittleEndian.Uint64(buf[40:48])
	h.ClusterPtrPos = binary.LittleEndian.Uint64(buf[48:56])
	h.MimeListPos = binary.LittleEndian.Uint64(buf[56:64])
	h.MainPage = binary.LittleEndian.Uint32(buf[64:68])
	h.LayoutPage = binary.LittleEndian.Uint32(buf[68:72])
	h.ChecksumPos = binary.LittleEndian.Uint64(buf[72:80])
	return h, nil
}

// readZimMimeList reads the list of MIME-type strings. Each is a
// null-terminated UTF-8 string; the list ends with an empty string
// (i.e. two consecutive nulls).
func readZimMimeList(ra io.ReaderAt, off int64) ([]string, error) {
	var all []byte
	pos := off
	for {
		var buf [4096]byte
		n, err := ra.ReadAt(buf[:], pos)
		if n > 0 {
			all = append(all, buf[:n]...)
		}
		if i := bytes.Index(all, []byte{0, 0}); i >= 0 {
			all = all[:i+1]
			break
		}
		if err != nil {
			break
		}
		pos += int64(n)
		if len(all) > zimMaxMimeListBytes {
			return nil, errors.New("zim: mime list exceeds cap")
		}
	}
	if len(all) == 0 {
		return nil, errors.New("zim: empty mime list")
	}
	parts := bytes.Split(all, []byte{0})
	mimes := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) > 0 {
			mimes = append(mimes, string(p))
		}
	}
	if len(mimes) == 0 {
		return nil, errors.New("zim: parsed zero mime types")
	}
	return mimes, nil
}

// zimDirEntry is the parsed form of a Content directory entry. Only
// the fields we need for extraction are populated.
type zimDirEntry struct {
	MimeIdx    uint16
	IsRedirect bool
	IsDeleted  bool
	ClusterNum uint32
	BlobNum    uint32
}

// readZimDirEntry resolves the i-th URL pointer and reads the first
// 16 bytes of the dir entry (everything before the variable-length
// url+title strings, which we don't need for extraction).
func readZimDirEntry(ra io.ReaderAt, hdr *zimHeader, i uint32) (zimDirEntry, error) {
	var ptrBuf [8]byte
	if _, err := ra.ReadAt(ptrBuf[:], int64(hdr.URLPtrPos)+int64(i)*8); err != nil {
		return zimDirEntry{}, fmt.Errorf("zim: read url ptr: %w", err)
	}
	dePos := int64(binary.LittleEndian.Uint64(ptrBuf[:]))

	var hdrBuf [16]byte
	if _, err := ra.ReadAt(hdrBuf[:], dePos); err != nil {
		return zimDirEntry{}, fmt.Errorf("zim: read dir entry: %w", err)
	}
	mimeIdx := binary.LittleEndian.Uint16(hdrBuf[0:2])
	switch mimeIdx {
	case zimRedirectMime, zimLinkTargetMime:
		return zimDirEntry{IsRedirect: true}, nil
	case zimDeletedMime:
		return zimDirEntry{IsDeleted: true}, nil
	}
	return zimDirEntry{
		MimeIdx:    mimeIdx,
		ClusterNum: binary.LittleEndian.Uint32(hdrBuf[8:12]),
		BlobNum:    binary.LittleEndian.Uint32(hdrBuf[12:16]),
	}, nil
}

// zstdDecoderOnce constructs the stateless zstd decoder lazily and
// shares it across calls. klauspost/compress's NewReader(nil) +
// DecodeAll path is documented as concurrency-safe.
var (
	zstdDecoder     *zstd.Decoder
	zstdDecoderOnce sync.Once
	zstdDecoderErr  error
)

func getZstdDecoder() (*zstd.Decoder, error) {
	zstdDecoderOnce.Do(func() {
		zstdDecoder, zstdDecoderErr = zstd.NewReader(nil,
			zstd.WithDecoderConcurrency(0),
			zstd.WithDecoderMaxMemory(zimMaxClusterBytes),
		)
	})
	return zstdDecoder, zstdDecoderErr
}

// readZimCluster fetches the raw cluster bytes, decompresses if
// needed, and returns the byte sequence
//
//	[type byte] [optionally-decompressed body]
//
// so the blob parser can read the extended-cluster bit from the type
// byte even on already-decompressed clusters.
func readZimCluster(ra io.ReaderAt, hdr *zimHeader, num uint32) ([]byte, error) {
	if num >= hdr.ClusterCount {
		return nil, fmt.Errorf("zim: cluster %d out of range (count=%d)", num, hdr.ClusterCount)
	}
	var startBuf [8]byte
	if _, err := ra.ReadAt(startBuf[:], int64(hdr.ClusterPtrPos)+int64(num)*8); err != nil {
		return nil, fmt.Errorf("zim: read cluster ptr: %w", err)
	}
	start := int64(binary.LittleEndian.Uint64(startBuf[:]))

	var end int64
	if num+1 < hdr.ClusterCount {
		var endBuf [8]byte
		if _, err := ra.ReadAt(endBuf[:], int64(hdr.ClusterPtrPos)+int64(num+1)*8); err != nil {
			return nil, fmt.Errorf("zim: read next cluster ptr: %w", err)
		}
		end = int64(binary.LittleEndian.Uint64(endBuf[:]))
	} else {
		end = int64(hdr.ChecksumPos)
	}
	if end <= start {
		return nil, fmt.Errorf("zim: cluster %d end %d ≤ start %d", num, end, start)
	}
	size := end - start
	if size > zimMaxClusterBytes {
		return nil, fmt.Errorf("zim: cluster %d size %d exceeds cap", num, size)
	}

	raw := make([]byte, size)
	if _, err := ra.ReadAt(raw, start); err != nil {
		return nil, fmt.Errorf("zim: read cluster %d: %w", num, err)
	}
	if len(raw) == 0 {
		return nil, errors.New("zim: empty cluster")
	}
	typeByte := raw[0]
	compType := typeByte & 0x0F
	body := raw[1:]

	switch compType {
	case zimCompUncompressed:
		out := make([]byte, 0, len(body)+1)
		out = append(out, typeByte)
		out = append(out, body...)
		return out, nil
	case zimCompZstd:
		dec, err := getZstdDecoder()
		if err != nil {
			return nil, fmt.Errorf("zim: zstd init: %w", err)
		}
		decoded, err := dec.DecodeAll(body, nil)
		if err != nil {
			return nil, fmt.Errorf("zim: zstd decode cluster %d: %w", num, err)
		}
		out := make([]byte, 0, len(decoded)+1)
		out = append(out, typeByte)
		out = append(out, decoded...)
		return out, nil
	case zimCompXZ:
		return nil, errors.New("zim: XZ/LZMA2 clusters not supported in v1")
	default:
		return nil, fmt.Errorf("zim: unknown cluster compression %d", compType)
	}
}

// getZimBlob slices blob #blobNum out of a (possibly decompressed)
// cluster. The cluster argument is [typeByte || body]; the type byte's
// extended bit (0x10) determines whether per-blob offsets are 4 or 8
// bytes wide.
func getZimBlob(cluster []byte, blobNum uint32) ([]byte, error) {
	if len(cluster) < 1 {
		return nil, errors.New("zim: cluster missing type byte")
	}
	extended := cluster[0]&0x10 != 0
	body := cluster[1:]

	offsetSize := 4
	if extended {
		offsetSize = 8
	}
	if len(body) < offsetSize {
		return nil, errors.New("zim: cluster body shorter than first offset")
	}

	var firstOffset uint64
	if extended {
		firstOffset = binary.LittleEndian.Uint64(body[:8])
	} else {
		firstOffset = uint64(binary.LittleEndian.Uint32(body[:4]))
	}
	// Number of (offset table) entries = firstOffset / offsetSize.
	// Number of blobs = entry count − 1.
	if firstOffset == 0 || firstOffset%uint64(offsetSize) != 0 {
		return nil, fmt.Errorf("zim: bad first offset %d", firstOffset)
	}
	blobCount := firstOffset/uint64(offsetSize) - 1
	if uint64(blobNum) >= blobCount {
		return nil, fmt.Errorf("zim: blob %d out of range (max %d)", blobNum, blobCount)
	}

	sIdx := int(blobNum) * offsetSize
	eIdx := (int(blobNum) + 1) * offsetSize
	if eIdx+offsetSize > len(body) {
		return nil, errors.New("zim: blob offset entry past cluster end")
	}
	var startOff, endOff uint64
	if extended {
		startOff = binary.LittleEndian.Uint64(body[sIdx : sIdx+8])
		endOff = binary.LittleEndian.Uint64(body[eIdx : eIdx+8])
	} else {
		startOff = uint64(binary.LittleEndian.Uint32(body[sIdx : sIdx+4]))
		endOff = uint64(binary.LittleEndian.Uint32(body[eIdx : eIdx+4]))
	}
	if startOff > endOff || endOff > uint64(len(body)) {
		return nil, fmt.Errorf("zim: blob %d offsets [%d,%d] invalid (cluster size %d)", blobNum, startOff, endOff, len(body))
	}
	return body[startOff:endOff], nil
}

// zimIsExtractableMime reports whether SwartzNet's text pipeline can
// usefully turn a blob with this MIME into searchable text.
func zimIsExtractableMime(m string) bool {
	switch {
	case strings.HasPrefix(m, "text/html"):
		return true
	case strings.HasPrefix(m, "application/xhtml"):
		return true
	case strings.HasPrefix(m, "text/plain"):
		return true
	}
	return false
}

// zimDecodeArticle turns a raw blob into searchable plaintext. HTML
// goes through the existing extractHTMLText helper; plain text is
// returned as-is (trimmed).
func zimDecodeArticle(blob []byte, mime string) string {
	if strings.HasPrefix(mime, "text/html") || strings.HasPrefix(mime, "application/xhtml") {
		text, err := extractHTMLText(bytes.NewReader(blob))
		if err != nil {
			return ""
		}
		return text
	}
	return strings.TrimSpace(string(blob))
}

func init() {
	Register(NewZimExtractor(), func(mime string, c Candidate) bool {
		if mime == "application/x-zim" || mime == "application/x-openzim" {
			return true
		}
		if strings.EqualFold(filepath.Ext(c.Path), ".zim") {
			return true
		}
		return false
	})
}
