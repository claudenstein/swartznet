// SyncSession: one end of an RIBLT set-reconciliation exchange
// between two sn_search peers. SPEC.md §2.2 state machine.
//
// Usage (initiator side):
//
//	s := NewSyncSession(txid, RoleInitiator, localRecords)
//	begin := s.Begin(filter)
//	// send `begin` (sync_begin)
//	// receive sync_symbols frames, feed them via s.ApplySymbols
//	// eventually s.NeedIDs() reports the decode result
//	// send sync_need, receive sync_records via s.ApplyRecords
//	// call s.Finish() to emit sync_end
//
// Responder side is symmetric: ApplyBegin, then ProduceSymbols
// until told to stop, ApplyNeed for fulfillment, ApplyEnd for
// teardown.
//
// This type carries no I/O — callers thread frames through. That
// keeps it unit-testable and agnostic to the LTEP wire layer.

package swarmsearch

import (
	"crypto/sha256"
	"errors"
	"fmt"
)

// SyncRole identifies which side of a session this is.
type SyncRole int

const (
	RoleInitiator SyncRole = 1
	RoleResponder SyncRole = 2
)

// SyncSessionPhase tracks the session's state machine position.
type SyncSessionPhase int

const (
	PhaseIdle SyncSessionPhase = iota
	PhaseBegun
	PhaseSymbolsFlowing
	PhaseNeeded
	PhaseFulfilled
	PhaseEnded
)

// LocalRecord is the wire-format-friendly view of a single
// signed Aggregate record. Callers map to/from their native
// companion.Record type. Keeping the session free of that
// import prevents an import cycle (dhtindex → companion → would
// eventually pull swarmsearch).
type LocalRecord struct {
	Pk  [32]byte
	Kw  string
	Ih  [20]byte
	T   int64
	Pow uint64
	Sig [64]byte
}

// SyncSession carries the full state of one RIBLT exchange.
type SyncSession struct {
	txid    uint32
	role    SyncRole
	phase   SyncSessionPhase
	filter  SyncFilter
	records map[[32]byte]LocalRecord // indexed by RIBLT element ID

	enc *RIBLTEncoder
	dec *RIBLTDecoder

	// Budget tracking. Session aborts with limit_exceeded when
	// either cap is crossed.
	maxSymbols int
	maxBytes   int

	// Observability.
	symbolsOut int
	symbolsIn  int
	bytesIn    int
	bytesOut   int

	// After decoding, a stable list of IDs we need records for.
	neededIDs [][32]byte
}

// NewSyncSession constructs a fresh session. `records` is the
// sender's local set; both sides pre-index it by RIBLT element ID
// so ApplyNeed can look up records in O(1).
func NewSyncSession(txid uint32, role SyncRole, records []LocalRecord) *SyncSession {
	idx := make(map[[32]byte]LocalRecord, len(records))
	enc := NewRIBLTEncoder()
	for _, r := range records {
		id := localRecordID(r)
		idx[id] = r
		enc.AddElement(id)
	}
	dec := NewRIBLTDecoder()
	for _, r := range records {
		dec.AddLocalElement(localRecordID(r))
	}
	return &SyncSession{
		txid:       txid,
		role:       role,
		phase:      PhaseIdle,
		records:    idx,
		enc:        enc,
		dec:        dec,
		maxSymbols: DefaultSyncMaxSymbols,
		maxBytes:   DefaultSyncMaxBytes,
	}
}

// TxID returns the session's transaction id.
func (s *SyncSession) TxID() uint32 { return s.txid }

// Phase returns the current state-machine position.
func (s *SyncSession) Phase() SyncSessionPhase { return s.phase }

// Role returns the role this session was constructed with.
func (s *SyncSession) Role() SyncRole { return s.role }

// SetBudgets overrides the default symbol/bytes caps.
func (s *SyncSession) SetBudgets(maxSymbols, maxBytes int) {
	if maxSymbols > 0 {
		s.maxSymbols = maxSymbols
	}
	if maxBytes > 0 {
		s.maxBytes = maxBytes
	}
}

// Begin produces the SyncBegin frame for the initiator. Returns
// an error if the session is in the wrong phase.
func (s *SyncSession) Begin(filter SyncFilter) (SyncBegin, error) {
	if s.role != RoleInitiator {
		return SyncBegin{}, errors.New("swarmsearch: Begin on non-initiator session")
	}
	if s.phase != PhaseIdle {
		return SyncBegin{}, fmt.Errorf("swarmsearch: Begin in phase %d", s.phase)
	}
	s.filter = filter
	s.phase = PhaseBegun
	return SyncBegin{
		TxID:        s.txid,
		Algo:        "riblt-v1",
		Filter:      filter,
		ElementSize: 32,
		LocalCount:  s.enc.Len(),
		MaxSymbols:  s.maxSymbols,
		MaxBytes:    s.maxBytes,
	}, nil
}

// ApplyBegin consumes a SyncBegin frame on the responder side.
// After this, callers should call ProduceSymbols to stream RIBLT
// symbols back.
func (s *SyncSession) ApplyBegin(m SyncBegin) error {
	if s.role != RoleResponder {
		return errors.New("swarmsearch: ApplyBegin on non-responder session")
	}
	if s.phase != PhaseIdle {
		return fmt.Errorf("swarmsearch: ApplyBegin in phase %d", s.phase)
	}
	if m.TxID != s.txid {
		return fmt.Errorf("swarmsearch: sync_begin txid %d, session expects %d",
			m.TxID, s.txid)
	}
	if m.ElementSize != 32 {
		return fmt.Errorf("swarmsearch: unsupported element_size %d", m.ElementSize)
	}
	s.filter = m.Filter
	if m.MaxSymbols > 0 && m.MaxSymbols < s.maxSymbols {
		s.maxSymbols = m.MaxSymbols
	}
	if m.MaxBytes > 0 && m.MaxBytes < s.maxBytes {
		s.maxBytes = m.MaxBytes
	}
	s.phase = PhaseBegun
	return nil
}

// ProduceSymbols emits up to `count` RIBLT symbols in one batch.
// Returned batch size is min(count, MaxSymbolsPerMessage). The
// caller should wrap the result into SyncSymbols and send. Phase
// advances to PhaseSymbolsFlowing.
func (s *SyncSession) ProduceSymbols(count int) ([]SyncSymbol, uint32, error) {
	if s.phase != PhaseBegun && s.phase != PhaseSymbolsFlowing {
		return nil, 0, fmt.Errorf("swarmsearch: ProduceSymbols in phase %d", s.phase)
	}
	if count <= 0 {
		count = MaxSymbolsPerMessage
	}
	if count > MaxSymbolsPerMessage {
		count = MaxSymbolsPerMessage
	}
	if s.symbolsOut+count > s.maxSymbols {
		count = s.maxSymbols - s.symbolsOut
		if count <= 0 {
			return nil, 0, ErrSymbolBudgetExceeded
		}
	}
	baseIdx := uint32(s.enc.NextSymbolIndex())
	out := make([]SyncSymbol, 0, count)
	for i := 0; i < count; i++ {
		sym := s.enc.NextSymbol()
		copiedData := make([]byte, 32)
		copy(copiedData, sym.DataXOR[:])
		out = append(out, SyncSymbol{
			Count:   sym.Count,
			KeyXOR:  sym.KeyXOR,
			DataXOR: copiedData,
		})
	}
	s.symbolsOut += len(out)
	s.phase = PhaseSymbolsFlowing
	return out, baseIdx, nil
}

// ApplySymbols ingests a SyncSymbols frame from the peer. Runs
// peeling internally; after the call, NeedIDs returns IDs the
// local side needs records for.
func (s *SyncSession) ApplySymbols(m SyncSymbols) error {
	if s.phase != PhaseBegun && s.phase != PhaseSymbolsFlowing {
		return fmt.Errorf("swarmsearch: ApplySymbols in phase %d", s.phase)
	}
	if m.TxID != s.txid {
		return fmt.Errorf("swarmsearch: sync_symbols txid %d, want %d", m.TxID, s.txid)
	}
	if s.symbolsIn+len(m.Symbols) > s.maxSymbols {
		return ErrSymbolBudgetExceeded
	}
	for _, ws := range m.Symbols {
		var sym RIBLTSymbol
		sym.Count = ws.Count
		sym.KeyXOR = ws.KeyXOR
		copy(sym.DataXOR[:], ws.DataXOR)
		s.dec.AddRemoteSymbol(sym)
	}
	s.symbolsIn += len(m.Symbols)
	s.phase = PhaseSymbolsFlowing
	return nil
}

// NeedIDs returns the element IDs decoded as "peer has, I lack".
// Result is stable: if called twice with no intervening
// ApplySymbols, returns the same set. Empty when no decoding has
// happened yet or when sets are already equal.
func (s *SyncSession) NeedIDs() [][32]byte {
	added := s.dec.Added()
	ids := make([][32]byte, 0, len(added))
	for _, e := range added {
		var id [32]byte
		copy(id[:], e[:])
		ids = append(ids, id)
	}
	s.neededIDs = ids
	return ids
}

// RemovedIDs returns the element IDs decoded as "I have, peer
// lacks". Caller may use this to decide whether to ALSO send the
// peer records (mirror flow).
func (s *SyncSession) RemovedIDs() [][32]byte {
	removed := s.dec.Removed()
	out := make([][32]byte, 0, len(removed))
	for _, e := range removed {
		var id [32]byte
		copy(id[:], e[:])
		out = append(out, id)
	}
	return out
}

// Converged reports whether the RIBLT decoder has zeroed out its
// residual diff — i.e., all differences are enumerated in
// NeedIDs + RemovedIDs.
func (s *SyncSession) Converged() bool { return s.dec.Converged() }

// NeedFrame produces the SyncNeed frame requesting records for
// the given IDs. Phase advances to PhaseNeeded. IDs may be empty
// to signal "I'm done decoding" per SPEC §2.6.
func (s *SyncSession) NeedFrame(ids [][32]byte) (SyncNeed, error) {
	if s.phase != PhaseSymbolsFlowing && s.phase != PhaseBegun {
		return SyncNeed{}, fmt.Errorf("swarmsearch: NeedFrame in phase %d", s.phase)
	}
	if len(ids) > MaxNeedIDsPerMessage {
		return SyncNeed{}, fmt.Errorf("swarmsearch: %d ids exceeds cap %d",
			len(ids), MaxNeedIDsPerMessage)
	}
	idSlices := make([][]byte, len(ids))
	for i, id := range ids {
		b := make([]byte, 32)
		copy(b, id[:])
		idSlices[i] = b
	}
	s.phase = PhaseNeeded
	return SyncNeed{TxID: s.txid, IDs: idSlices}, nil
}

// ApplyNeed processes an incoming SyncNeed, returning records
// matching the requested IDs. Unknown IDs (we don't have records
// for them) land in the `missing` return.
func (s *SyncSession) ApplyNeed(m SyncNeed) (records []LocalRecord, missing [][32]byte, err error) {
	if m.TxID != s.txid {
		return nil, nil, fmt.Errorf("swarmsearch: sync_need txid %d, want %d", m.TxID, s.txid)
	}
	if len(m.IDs) > MaxNeedIDsPerMessage {
		return nil, nil, fmt.Errorf("swarmsearch: sync_need has %d ids (cap %d)",
			len(m.IDs), MaxNeedIDsPerMessage)
	}
	for _, raw := range m.IDs {
		if len(raw) != 32 {
			return nil, nil, fmt.Errorf("swarmsearch: sync_need id %d bytes", len(raw))
		}
		var id [32]byte
		copy(id[:], raw)
		if r, ok := s.records[id]; ok {
			records = append(records, r)
		} else {
			missing = append(missing, id)
		}
	}
	return records, missing, nil
}

// BuildRecordsFrame emits a SyncRecords frame carrying the given
// records. Caller is responsible for chunking when len > cap.
func (s *SyncSession) BuildRecordsFrame(recs []LocalRecord, missing [][32]byte) (SyncRecords, error) {
	if len(recs) > MaxRecordsPerMessage {
		return SyncRecords{}, fmt.Errorf("swarmsearch: %d records exceeds cap %d",
			len(recs), MaxRecordsPerMessage)
	}
	wireRecs := make([]SyncRecord, 0, len(recs))
	for _, r := range recs {
		wireRecs = append(wireRecs, SyncRecord{
			Pk:  append([]byte(nil), r.Pk[:]...),
			Kw:  r.Kw,
			Ih:  append([]byte(nil), r.Ih[:]...),
			T:   r.T,
			Pow: r.Pow,
			Sig: append([]byte(nil), r.Sig[:]...),
		})
	}
	missingSlices := make([][]byte, 0, len(missing))
	for _, id := range missing {
		b := make([]byte, 32)
		copy(b, id[:])
		missingSlices = append(missingSlices, b)
	}
	s.phase = PhaseFulfilled
	return SyncRecords{
		TxID:    s.txid,
		Records: wireRecs,
		Missing: missingSlices,
	}, nil
}

// ApplyRecords consumes a SyncRecords frame. Returns the records
// that were newly learned (for caller-side ingestion). The caller
// is responsible for verifying per-record signatures + PoW and
// handing them off to the indexer.
func (s *SyncSession) ApplyRecords(m SyncRecords) ([]SyncRecord, error) {
	if m.TxID != s.txid {
		return nil, fmt.Errorf("swarmsearch: sync_records txid %d, want %d", m.TxID, s.txid)
	}
	// SPEC §2.7: receiver re-verifies sigs. This session wrapper
	// treats records as opaque — it's the dhtindex/companion
	// layer that knows how to verify. We still range-check sizes
	// as the wire-level guard.
	for i, r := range m.Records {
		if len(r.Pk) != 32 || len(r.Ih) != 20 || len(r.Sig) != 64 {
			return nil, fmt.Errorf("swarmsearch: sync_records record[%d] bad sizes", i)
		}
	}
	s.phase = PhaseFulfilled
	return m.Records, nil
}

// Finish emits a SyncEnd frame terminating the session.
func (s *SyncSession) Finish(status string) SyncEnd {
	s.phase = PhaseEnded
	if status == "" {
		status = SyncStatusConverged
	}
	return SyncEnd{
		TxID:     s.txid,
		Status:   status,
		Sent:     s.symbolsOut,
		BytesIn:  s.bytesIn,
		BytesOut: s.bytesOut,
	}
}

// ApplyEnd consumes an incoming SyncEnd and closes the session.
func (s *SyncSession) ApplyEnd(m SyncEnd) error {
	if m.TxID != s.txid {
		return fmt.Errorf("swarmsearch: sync_end txid %d, want %d", m.TxID, s.txid)
	}
	s.phase = PhaseEnded
	return nil
}

// RecordByID returns the local record matching the given RIBLT
// element ID, or ok=false if absent. Used by handler.go when a
// peer sends a sync_need we must respond to.
func (s *SyncSession) RecordByID(id [32]byte) (LocalRecord, bool) {
	r, ok := s.records[id]
	return r, ok
}

// localRecordID derives the 32-byte RIBLT element ID from a
// LocalRecord by SHA-256-ing the canonical sign message. Matches
// SPEC.md §2.4 exactly so both peers converge on the same id
// for the same record.
func localRecordID(r LocalRecord) [32]byte {
	msg := make([]byte, 0, 32+len(r.Kw)+20+8)
	msg = append(msg, r.Pk[:]...)
	msg = append(msg, r.Kw...)
	msg = append(msg, r.Ih[:]...)
	var ts [8]byte
	for i := 0; i < 8; i++ {
		ts[i] = byte(r.T >> (8 * i))
	}
	msg = append(msg, ts[:]...)
	return sha256.Sum256(msg)
}
