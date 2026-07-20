package admission

// Source tags where a candidate publisher was observed. The vocabulary
// is frozen: it matches the legacy candidate.Source values and the
// Slice 12 feeder channels (anchor PPMI, BEP-51 crawl, endorsement
// gossip) map one-to-one onto these tags.
type Source string

const (
	// SourceAnchor is a project trust-anchor pubkey (channel A).
	SourceAnchor Source = "anchor"
	// SourceBEP51 is a publisher discovered via a BEP-51 crawl whose
	// snet.sig was verified upstream before admission (channel B).
	SourceBEP51 Source = "bep51"
	// SourceEndorsement is a publisher vouched for via peer_announce
	// endorsement gossip (channel C).
	SourceEndorsement Source = "endorsement"
)

const (
	// UnknownPrior is the neutral reputation score of a publisher with
	// no record. Admission's earned-signal bar is STRICTLY above this
	// value: a fresh 0.5 is never "strong". This is the constant the
	// legacy defect leaned on (0.5 >= 0.3 and 0.5 >= 0.5 both admitted).
	UnknownPrior = 0.5

	// MaxTrackedPublishers caps how many non-anchor candidates the
	// engine admits. Anchors are exempt (see AdmissionEngine.admit).
	MaxTrackedPublishers = 100

	// EndorsementThreshold is the number of distinct STRONG endorsers
	// required to admit an unproven candidate on endorsements alone.
	EndorsementThreshold = 3
)

// DefaultAnchorPubkeys is the SwartzNet project's trust-anchor pubkey
// list. It ships EMPTY: a curated anchor set / seeds.json is a release
// prerequisite, not a code artifact. A zero-anchor engine is correct
// but INERT — discovery admits nobody until operators supply real
// keys. Hardcoding keys here would reintroduce a fake trust root, so
// it stays empty; operators inject their own via the constructor.
var DefaultAnchorPubkeys = []string{}

// ReputationView is the narrow reputation surface the admission gate
// reads. It is keyed on the 32-byte pubkey so admission stays a leaf
// package with no compile dependency on internal/reputation; the
// daemon adapts a *reputation.Tracker to it (Score, IsSeeded,
// MarkSeeded forward through reputation.PubKey). A nil view carries no
// earned signal, which keeps the gate fail-closed.
type ReputationView interface {
	// Score returns the derived reputation in [0,1]; unknown pubkeys
	// return the neutral UnknownPrior.
	Score(pub [32]byte) float64
	// IsSeeded reports whether the pubkey carries a seed/anchor bonus.
	IsSeeded(pub [32]byte) bool
	// MarkSeeded records the pubkey as a seed (anchor admit only).
	MarkSeeded(pub [32]byte, label string)
}

// Candidate is one publisher under consideration for admission.
// StrongEndorsers is populated by the engine before a Rule sees the
// candidate; it is the count of distinct endorsers whose earned signal
// clears the strong bar.
type Candidate struct {
	Pub             [32]byte
	Label           string
	Source          Source
	StrongEndorsers int
}

// earned reports whether a pubkey carries positive earned signal:
// strictly above the unknown prior, or a seed/anchor membership. A
// fresh 0.5-scored, unseeded pubkey is NOT earned. A nil view is never
// earned (fail-closed).
func earned(pub [32]byte, v ReputationView) bool {
	if v == nil {
		return false
	}
	return v.Score(pub) > UnknownPrior || v.IsSeeded(pub)
}

// Rule is one deny-by-default admission predicate governing a single
// candidate Source. Admit reports true ONLY when the rule
// affirmatively clears the candidate; every unmatched or unproven case
// is denied. Rules are pure — they never mutate state.
type Rule interface {
	// Source is the candidate source this rule governs.
	Source() Source
	// Admit reports whether the candidate clears this rule.
	Admit(c Candidate, v ReputationView) bool
}

// AnchorRule admits anchor-sourced candidates unconditionally: anchors
// are the trust seeds the whole policy leans on. Capacity exemption and
// seed-marking are handled by the engine, not the rule.
type AnchorRule struct{}

func (AnchorRule) Source() Source                       { return SourceAnchor }
func (AnchorRule) Admit(Candidate, ReputationView) bool { return true }

// EndorsementRule admits an endorsement-sourced candidate only with
// enough STRONG endorsers, or its own earned/seeded signal. A fresh
// candidate backed only by fresh 0.5-scored endorsers is denied — this
// is the Sybil hole the legacy `Score >= 0.5` endorser count left open.
type EndorsementRule struct{ Threshold int }

func (EndorsementRule) Source() Source { return SourceEndorsement }

func (r EndorsementRule) Admit(c Candidate, v ReputationView) bool {
	return c.StrongEndorsers >= r.Threshold || earned(c.Pub, v)
}

// CrawlRule admits a BEP-51 crawl candidate only with its own
// earned/seeded signal, or enough strong endorsers. An unknown 0.5
// candidate is denied — this is the `Score >= 0.3` hole the legacy
// bloomPolicy left open. Signature validity is a precondition enforced
// by the engine before the rule runs, not a policy predicate.
type CrawlRule struct{ Threshold int }

func (CrawlRule) Source() Source { return SourceBEP51 }

func (r CrawlRule) Admit(c Candidate, v ReputationView) bool {
	return earned(c.Pub, v) || c.StrongEndorsers >= r.Threshold
}

// permissiveRule admits everything. It backs PermissivePolicyForTest
// and is unexported so no production caller can compose it.
type permissiveRule struct{ src Source }

func (r permissiveRule) Source() Source                     { return r.src }
func (permissiveRule) Admit(Candidate, ReputationView) bool { return true }

// Policy is a deny-by-default composition of typed rules keyed by the
// source they govern. Its rule set is unexported so the only way to
// obtain a Policy is DefaultPolicy (deny) or PermissivePolicyForTest —
// production wiring structurally cannot build a permissive policy.
type Policy struct {
	MaxTrackedPublishers int
	EndorsementThreshold int
	rules                map[Source]Rule
}

// ruleFor returns the rule governing src, or nil when no rule handles
// the source (an unknown source is denied by default).
func (p Policy) ruleFor(src Source) Rule {
	return p.rules[src]
}

// DefaultPolicy is the production policy: deny-by-default. An unknown
// 0.5-scored candidate is not admitted, and fresh 0.5-scored endorsers
// do not clear the endorsement bar. Deny is the composed default when
// no rule affirmatively admits.
func DefaultPolicy() Policy {
	return Policy{
		MaxTrackedPublishers: MaxTrackedPublishers,
		EndorsementThreshold: EndorsementThreshold,
		rules: map[Source]Rule{
			SourceAnchor:      AnchorRule{},
			SourceEndorsement: EndorsementRule{Threshold: EndorsementThreshold},
			SourceBEP51:       CrawlRule{Threshold: EndorsementThreshold},
		},
	}
}

// PermissivePolicyForTest returns the legacy always-admit policy. It is
// TEST-ONLY: it must never be wired into daemon.New. The deterministic
// deny gate owns the production state transition; this permissive path
// exists solely so tests can exercise capacity/bookkeeping without
// having to manufacture earned reputation.
func PermissivePolicyForTest() Policy {
	return Policy{
		MaxTrackedPublishers: MaxTrackedPublishers,
		EndorsementThreshold: EndorsementThreshold,
		rules: map[Source]Rule{
			SourceAnchor:      permissiveRule{SourceAnchor},
			SourceEndorsement: permissiveRule{SourceEndorsement},
			SourceBEP51:       permissiveRule{SourceBEP51},
		},
	}
}
