package swarmsearch

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/swartznet/swartznet/contracts/ltepwire"
	"github.com/swartznet/swartznet/contracts/riblt"
)

// Sync roles and phases.
type syncRole int

const (
	RoleInitiator syncRole = 1
	RoleResponder syncRole = 2
)

type syncPhase int

const (
	PhaseIdle syncPhase = iota
	PhaseBegun
	PhaseSymbolsFlowing
	PhaseNeeded
	PhaseFulfilled
	PhaseEnded
)

// Sync session errors. ErrSymbolBudgetExceeded → sync_end "limit_exceeded"
// (penalty-free); every other error → "aborted" (+ misbehavior charge).
var (
	ErrSymbolBudgetExceeded    = errors.New("swarmsearch: RIBLT symbol budget exceeded")
	ErrSyncBytesBudgetExceeded = errors.New("swarmsearch: sync byte budget exceeded")
	ErrSyncDesync              = errors.New("swarmsearch: sync symbol index desync")
	ErrSyncPhase               = errors.New("swarmsearch: illegal sync phase transition")
	ErrSyncTxID                = errors.New("swarmsearch: sync txid mismatch")
	ErrSyncTooLarge            = errors.New("swarmsearch: sync frame over cap")
)

// SyncSession is one RIBLT reconciliation exchange with a peer, keyed by txid.
// I/O-free: it produces/consumes ltepwire frames; the caller sends them. The
// local record set is snapshotted at construction — a live RecordCache.Add does
// NOT enter an in-flight session.
type SyncSession struct {
	mu    sync.Mutex
	txid  uint32
	role  syncRole
	phase syncPhase

	records map[[32]byte]LocalRecord // by ElementID, for ApplyNeed lookups
	enc     riblt.Encoder
	dec     *riblt.Decoder

	maxSymbols int
	maxBytes   int
	symbolsOut int
	symbolsIn  int
	bytesIn    int
	bytesOut   int
	recordsIn  int

	// pendingSymbols buffers out-of-order symbol batches by their stream index
	// so the async per-frame dispatch (goroutine per inbound frame) does not
	// trip the strict index==symbolsIn check. doneReceived records the pump's
	// terminal done=1. convergeFired is the initiator's once-guard.
	pendingSymbols map[int]ltepwire.SyncSymbols
	doneReceived   bool
	convergeFired  bool

	lastActivity time.Time
	stopPumpCh   chan struct{}
	stopOnce     sync.Once
}

// convergeSymbolFloor is the minimum symbols the initiator applies before it
// trusts Converged() — rateless "all residual symbols zero" is trivially true
// on a too-short prefix, so an element that first contributes past a short
// prefix would be silently lost. 256 makes the escape probability ~2^-21.
const convergeSymbolFloor = 256

// maxPendingSymbolBatches bounds the reorder buffer; a gap larger than this
// (a genuinely lost batch) aborts the session rather than buffering forever.
const maxPendingSymbolBatches = 32

// touch updates the last-activity time (for lazy reaping). Caller holds mu.
func (s *SyncSession) touch() { s.lastActivity = time.Now() }

// stale reports whether the session has been idle longer than d.
func (s *SyncSession) stale(now time.Time, d time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return now.Sub(s.lastActivity) > d
}

// StopPump signals a responder's symbol pump to stop (the initiator converged
// and sent sync_need). Idempotent.
func (s *SyncSession) StopPump() { s.stopOnce.Do(func() { close(s.stopPumpCh) }) }

// pumpDone is the channel the pump selects on to observe StopPump.
func (s *SyncSession) pumpDone() <-chan struct{} { return s.stopPumpCh }

// NewSyncSession snapshots records into an indexed map + seeded encoder/decoder.
func NewSyncSession(txid uint32, role syncRole, records []LocalRecord) *SyncSession {
	s := &SyncSession{
		txid:         txid,
		role:         role,
		phase:        PhaseIdle,
		records:      make(map[[32]byte]LocalRecord, len(records)),
		dec:          riblt.NewDecoder(),
		maxSymbols:   ltepwire.DefaultSyncMaxSymbols,
		maxBytes:     ltepwire.DefaultSyncMaxBytes,
		lastActivity: time.Now(),
		stopPumpCh:   make(chan struct{}),
	}
	for _, r := range records {
		id := r.ElementID()
		if _, dup := s.records[id]; dup {
			continue
		}
		s.records[id] = r
		el := riblt.RIBLTElement(id)
		s.enc.AddElement(el)
		s.dec.AddLocalElement(el)
	}
	return s
}

// TxID returns the session's transaction id.
func (s *SyncSession) TxID() uint32 { return s.txid }

// Phase returns the current phase (test/observability).
func (s *SyncSession) Phase() syncPhase {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phase
}

// Begin (initiator) emits the sync_begin frame. Idle→Begun.
func (s *SyncSession) Begin(filter ltepwire.SyncFilter) ltepwire.SyncBegin {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase = PhaseBegun
	return ltepwire.SyncBegin{
		TxID:        s.txid,
		Algo:        ltepwire.SyncAlgo,
		ElementSize: ltepwire.RIBLTElementSize,
		Filter:      filter,
		LocalCount:  s.enc.Len(),
		MaxSymbols:  s.maxSymbols,
		MaxBytes:    s.maxBytes,
	}
}

// ApplyBegin (responder) records the peer's begin and negotiates the budgets
// DOWNWARD (min of peer's and own). Idle→Begun.
func (s *SyncSession) ApplyBegin(m ltepwire.SyncBegin) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.TxID != s.txid {
		return ErrSyncTxID
	}
	if m.ElementSize != ltepwire.RIBLTElementSize {
		return ErrSyncDesync
	}
	if m.MaxSymbols > 0 && m.MaxSymbols < s.maxSymbols {
		s.maxSymbols = m.MaxSymbols
	}
	if m.MaxBytes > 0 && m.MaxBytes < s.maxBytes {
		s.maxBytes = m.MaxBytes
	}
	s.phase = PhaseBegun
	return nil
}

// ProduceSymbols (sender) emits the next batch (≤100, ≤ remaining budget).
func (s *SyncSession) ProduceSymbols(count int) (ltepwire.SyncSymbols, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if count > ltepwire.MaxSymbolsPerMessage {
		count = ltepwire.MaxSymbolsPerMessage
	}
	if remaining := s.maxSymbols - s.symbolsOut; count > remaining {
		count = remaining
	}
	if count <= 0 {
		return ltepwire.SyncSymbols{}, ErrSymbolBudgetExceeded
	}
	baseIdx := int(s.enc.NextSymbolIndex())
	syms := make([]ltepwire.SyncSymbol, count)
	for i := 0; i < count; i++ {
		cs := s.enc.NextSymbol()
		b := cs.DataXOR
		syms[i] = ltepwire.SyncSymbol{C: cs.Count, H: cs.KeyXOR, B: b[:]}
	}
	s.symbolsOut += count
	s.phase = PhaseSymbolsFlowing
	return ltepwire.SyncSymbols{TxID: s.txid, Index: baseIdx, Symbols: syms}, nil
}

// SymbolsOut / MaxSymbols expose budget state (for the pump).
func (s *SyncSession) SymbolsOut() int { s.mu.Lock(); defer s.mu.Unlock(); return s.symbolsOut }
func (s *SyncSession) MaxSymbols() int { s.mu.Lock(); defer s.mu.Unlock(); return s.maxSymbols }

// ApplySymbols (receiver) folds a batch into the decoder. It tolerates
// reordering: a batch ahead of the stream position is buffered; a stale/
// duplicate batch behind it is dropped idempotently; the contiguous prefix is
// applied in order. A gap larger than the reorder buffer (a lost batch) aborts.
func (s *SyncSession) ApplySymbols(m ltepwire.SyncSymbols) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.TxID != s.txid {
		return ErrSyncTxID
	}
	s.phase = PhaseSymbolsFlowing
	if m.Index < s.symbolsIn {
		return nil // stale/duplicate — idempotent drop
	}
	if m.Index > s.symbolsIn {
		if s.pendingSymbols == nil {
			s.pendingSymbols = make(map[int]ltepwire.SyncSymbols)
		}
		if _, dup := s.pendingSymbols[m.Index]; dup {
			return nil
		}
		if len(s.pendingSymbols) >= maxPendingSymbolBatches {
			return ErrSyncDesync // gap too large — a batch was lost
		}
		s.pendingSymbols[m.Index] = m
		return nil
	}
	// m.Index == symbolsIn: apply this batch and drain contiguous buffered ones.
	cur := m
	for {
		if s.symbolsIn+len(cur.Symbols) > s.maxSymbols {
			return ErrSymbolBudgetExceeded
		}
		for _, w := range cur.Symbols {
			var data [32]byte
			copy(data[:], w.B)
			s.dec.AddRemoteSymbol(riblt.Symbol{Count: w.C, KeyXOR: w.H, DataXOR: data})
		}
		s.symbolsIn += len(cur.Symbols)
		if cur.Done {
			s.doneReceived = true
		}
		next, ok := s.pendingSymbols[s.symbolsIn]
		if !ok {
			break
		}
		delete(s.pendingSymbols, s.symbolsIn)
		cur = next
	}
	return nil
}

// ShouldInitiatorConverge reports (at most ONCE — the once-guard) whether the
// initiator should now finalize: the decoder resolved the diff AND enough
// symbols were seen to trust it, OR the responder signaled it is done
// streaming (its budget was exhausted). This prevents both premature
// convergence on a short prefix and re-firing on every post-convergence batch.
func (s *SyncSession) ShouldInitiatorConverge() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.convergeFired {
		return false
	}
	ready := (s.dec.Converged() && s.symbolsIn >= convergeSymbolFloor) || s.doneReceived
	if ready {
		s.convergeFired = true
	}
	return ready
}

// Converged reports whether the decoder has resolved the full difference. NOTE:
// this is trivially true on a too-short prefix — for the initiator's finalize
// decision use ShouldInitiatorConverge / Finalized, which apply the floor.
func (s *SyncSession) Converged() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dec.Converged()
}

// Finalized reports whether the initiator has fired its (once-only) convergence
// — i.e. the reconciliation request/push has been issued. This is the safe
// signal for a caller to wait on before closing the session.
func (s *SyncSession) Finalized() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.convergeFired
}

// NeedIDs returns the sorted ElementIDs the peer has that we lack (→ sync_need).
func (s *SyncSession) NeedIDs() [][32]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	added := s.dec.Added()
	out := make([][32]byte, len(added))
	for i, e := range added {
		out[i] = e
	}
	sort.Slice(out, func(i, j int) bool { return less32(out[i], out[j]) })
	return out
}

// RemovedRecords returns the local records the peer lacks (→ proactive push).
func (s *SyncSession) RemovedRecords() []LocalRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []LocalRecord
	for _, e := range s.dec.Removed() {
		if r, ok := s.records[e]; ok {
			out = append(out, r)
		}
	}
	return out
}

// NeedFrame (receiver) builds a sync_need. Flowing/Begun→Needed.
func (s *SyncSession) NeedFrame(ids [][32]byte) (ltepwire.SyncNeed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(ids) > ltepwire.MaxNeedIDsPerMessage {
		return ltepwire.SyncNeed{}, ErrSyncTooLarge
	}
	wire := make([][]byte, len(ids))
	for i, id := range ids {
		b := id
		wire[i] = b[:]
	}
	s.phase = PhaseNeeded
	return ltepwire.SyncNeed{TxID: s.txid, IDs: wire}, nil
}

// ApplyNeed (responder) resolves requested ids into (found records, missing
// ids). No phase change.
func (s *SyncSession) ApplyNeed(m ltepwire.SyncNeed) ([]LocalRecord, [][32]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.TxID != s.txid {
		return nil, nil, ErrSyncTxID
	}
	if len(m.IDs) > ltepwire.MaxNeedIDsPerMessage {
		return nil, nil, ErrSyncTooLarge
	}
	var found []LocalRecord
	var missing [][32]byte
	for _, idb := range m.IDs {
		if len(idb) != 32 {
			continue
		}
		var id [32]byte
		copy(id[:], idb)
		if r, ok := s.records[id]; ok {
			found = append(found, r)
		} else {
			missing = append(missing, id)
		}
	}
	return found, missing, nil
}

// BuildRecordsFrame (responder) builds a sync_records reply, accounting bytes
// on the semantic record size. →Fulfilled.
func (s *SyncSession) BuildRecordsFrame(recs []LocalRecord, missing [][32]byte) (ltepwire.SyncRecords, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(recs) > ltepwire.MaxRecordsPerMessage {
		return ltepwire.SyncRecords{}, ErrSyncTooLarge
	}
	wire := make([]ltepwire.SyncRecord, len(recs))
	for i, r := range recs {
		wire[i] = toWireRecord(r)
		s.bytesOut += syncRecordWireSize(r)
	}
	miss := make([][]byte, len(missing))
	for i, id := range missing {
		b := id
		miss[i] = b[:]
		s.bytesOut += 32
	}
	s.phase = PhaseFulfilled
	return ltepwire.SyncRecords{TxID: s.txid, Records: wire, Missing: miss}, nil
}

// ApplyRecords (receiver) accepts a sync_records frame (records treated opaque;
// signatures verified by the caller). Legal ONLY in Needed/Fulfilled. Bytes
// accounted on the semantic size. →Fulfilled.
func (s *SyncSession) ApplyRecords(m ltepwire.SyncRecords) ([]LocalRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.TxID != s.txid {
		return nil, ErrSyncTxID
	}
	// Records are illegal only BEFORE the RIBLT exchange (an unsolicited dump
	// straight after sync_begin). Once symbols are flowing — or we sent
	// sync_need — a proactive push is expected. This is broader than the
	// legacy "Needed/Fulfilled only" because the rebuild dispatches inbound
	// frames on independent goroutines, so a pushed sync_records can be
	// processed while the responder is still SymbolsFlowing (DECISIONS S8).
	if s.phase == PhaseIdle || s.phase == PhaseBegun {
		return nil, ErrSyncPhase
	}
	if len(m.Records) > ltepwire.MaxRecordsPerMessage {
		return nil, ErrSyncTooLarge
	}
	frameBytes := 0
	out := make([]LocalRecord, 0, len(m.Records))
	for _, w := range m.Records {
		if len(w.Pk) != 32 || len(w.Ih) != 20 || len(w.Sig) != 64 {
			return nil, ErrSyncPhase
		}
		r := fromWireRecord(w)
		frameBytes += syncRecordWireSize(r)
		out = append(out, r)
	}
	frameBytes += 32 * len(m.Missing)
	if s.bytesIn+frameBytes > s.maxBytes {
		return nil, ErrSyncBytesBudgetExceeded
	}
	s.bytesIn += frameBytes
	s.recordsIn += len(out)
	s.phase = PhaseFulfilled
	return out, nil
}

// Finish emits a sync_end. any→Ended.
func (s *SyncSession) Finish(status string) ltepwire.SyncEnd {
	s.mu.Lock()
	defer s.mu.Unlock()
	if status == "" {
		status = ltepwire.SyncStatusConverged
	}
	s.phase = PhaseEnded
	return ltepwire.SyncEnd{
		TxID:     s.txid,
		Status:   status,
		Decoded:  s.recordsIn,
		Sent:     s.symbolsOut,
		BytesIn:  s.bytesIn,
		BytesOut: s.bytesOut,
	}
}

// ApplyEnd marks the session ended.
func (s *SyncSession) ApplyEnd(m ltepwire.SyncEnd) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.TxID != s.txid {
		return ErrSyncTxID
	}
	s.phase = PhaseEnded
	return nil
}

func toWireRecord(r LocalRecord) ltepwire.SyncRecord {
	pk := r.Pk
	ih := r.Ih
	sig := r.Sig
	return ltepwire.SyncRecord{Pk: pk[:], Kw: r.Kw, Ih: ih[:], T: r.T, Pow: r.Pow, Sig: sig[:]}
}

func fromWireRecord(w ltepwire.SyncRecord) LocalRecord {
	var r LocalRecord
	copy(r.Pk[:], w.Pk)
	copy(r.Ih[:], w.Ih)
	copy(r.Sig[:], w.Sig)
	r.Kw = w.Kw
	r.T = w.T
	r.Pow = w.Pow
	return r
}

func less32(a, b [32]byte) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
