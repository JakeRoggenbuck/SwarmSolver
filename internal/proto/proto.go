// Package proto defines the wire format shared by the server, agents,
// load clients and dashboard: one JSON envelope per event.
package proto

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Topics. A client subscribes to any subset.
const (
	TopicLeads    = "leads"    // unverified claims, instant
	TopicVerified = "verified" // claims proven by the harness
	TopicVerdicts = "verdicts" // verifier outcomes (verified / refuted / unknown / skipped)
	TopicState    = "state"    // bound changes + snapshots
)

// Topic bitmask for cheap per-client filtering in the fan-out path.
const (
	MaskLeads uint8 = 1 << iota
	MaskVerified
	MaskVerdicts
	MaskState
	MaskAll = MaskLeads | MaskVerified | MaskVerdicts | MaskState
)

func TopicMask(t string) uint8 {
	switch t {
	case TopicLeads:
		return MaskLeads
	case TopicVerified:
		return MaskVerified
	case TopicVerdicts:
		return MaskVerdicts
	case TopicState:
		return MaskState
	}
	return 0
}

// ParseTopics turns "leads,verified" into a mask (empty = all).
func ParseTopics(s string) uint8 {
	if strings.TrimSpace(s) == "" {
		return MaskAll
	}
	var m uint8
	for _, t := range strings.Split(s, ",") {
		m |= TopicMask(strings.TrimSpace(t))
	}
	return m
}

// Claim kinds.
const (
	KindColoring      = "COLORING"       // chi <= k, certificate: full coloring
	KindClique        = "CLIQUE"         // chi >= k, certificate: k vertices
	KindSubgraphBound = "SUBGRAPH_BOUND" // chi >= k, certificate: S with G[S] not (k-1)-colorable
	KindSeparation    = "SEPARATION"     // every k-coloring has c(u) != c(v), certificate: S
	KindMerge         = "MERGE"          // heuristic lead: some optimal coloring has c(u) = c(v)
	KindNote          = "NOTE"           // free-form lead (strategy chatter, load test traffic)

	// Server-generated kinds.
	KindVerdict  = "VERDICT"
	KindBounds   = "BOUNDS"
	KindSnapshot = "SNAPSHOT"
)

// Verdict statuses.
const (
	StatusVerified = "verified"
	StatusRefuted  = "refuted"
	StatusUnknown  = "unknown"
	StatusSkipped  = "skipped" // valid-looking but cannot improve anything; not worth verifier time
)

// Claim is the certificate payload. Fields used depend on Kind.
type Claim struct {
	K        int   `json:"k"`
	U        *int  `json:"u,omitempty"`
	V        *int  `json:"v,omitempty"`
	S        []int `json:"S,omitempty"`
	Coloring []int `json:"coloring,omitempty"`
}

// Envelope is the single event type on the log.
type Envelope struct {
	Seq     uint64          `json:"seq"`
	Topic   string          `json:"topic"`
	Agent   string          `json:"agent,omitempty"`
	TS      int64           `json:"ts"` // unix millis, stamped by the server
	Kind    string          `json:"kind"`
	ID      string          `json:"id,omitempty"`
	Parents []string        `json:"parents,omitempty"`
	Claim   *Claim          `json:"claim,omitempty"`
	Note    string          `json:"note,omitempty"`
	Verdict *Verdict        `json:"verdict,omitempty"`
	Bounds  *Bounds         `json:"bounds,omitempty"`
	State   json.RawMessage `json:"state,omitempty"`
	// SentTS is set by publishers (optional) for end-to-end latency measurement.
	SentTS int64 `json:"sent_ts,omitempty"`
}

// Verdict is the harness outcome for one claim.
type Verdict struct {
	Of      string   `json:"of"`
	Kind    string   `json:"kind"`
	K       int      `json:"k"`
	Agent   string   `json:"agent"`
	Status  string   `json:"status"`
	Reason  string   `json:"reason,omitempty"`
	Engine  string   `json:"engine,omitempty"` // edge-scan, pair-scan, z3, native, cache
	Ms      float64  `json:"ms"`
	Core    int      `json:"core,omitempty"` // vertices left after k-core pruning (what the solver saw)
	Counter [][2]int `json:"counter,omitempty"`
}

// Bounds is the compact state event emitted whenever a bound moves.
type Bounds struct {
	Upper      int    `json:"upper"`
	Lower      int    `json:"lower"`
	UpperAgent string `json:"upper_agent,omitempty"`
	LowerAgent string `json:"lower_agent,omitempty"`
	LowerKind  string `json:"lower_kind,omitempty"`
	Cause      string `json:"cause,omitempty"` // claim id that moved the bound
}

func Int(v int) *int { return &v }

// Normalize canonicalizes a claim in place so that equal claims hash equal:
// sets sorted and deduplicated, u < v, colorings relabeled by first appearance,
// and k derived from the certificate where it is implied.
func Normalize(kind string, c *Claim) error {
	if c == nil {
		return fmt.Errorf("missing claim")
	}
	c.S = sortedSet(c.S)
	switch kind {
	case KindColoring:
		if len(c.Coloring) == 0 {
			return fmt.Errorf("COLORING needs a coloring")
		}
		m := map[int]int{}
		out := make([]int, len(c.Coloring))
		for i, x := range c.Coloring {
			if _, ok := m[x]; !ok {
				m[x] = len(m)
			}
			out[i] = m[x]
		}
		c.Coloring = out
		c.K = len(m)
		c.S, c.U, c.V = nil, nil, nil
	case KindClique:
		if len(c.S) == 0 {
			return fmt.Errorf("CLIQUE needs S")
		}
		c.K = len(c.S)
		c.Coloring, c.U, c.V = nil, nil, nil
	case KindSubgraphBound:
		if len(c.S) == 0 || c.K < 2 {
			return fmt.Errorf("SUBGRAPH_BOUND needs S and k >= 2")
		}
		c.Coloring, c.U, c.V = nil, nil, nil
	case KindSeparation, KindMerge:
		if c.U == nil || c.V == nil {
			return fmt.Errorf("%s needs u and v", kind)
		}
		if *c.U == *c.V {
			return fmt.Errorf("u == v")
		}
		if *c.U > *c.V {
			c.U, c.V = c.V, c.U
		}
		if kind == KindSeparation && (len(c.S) == 0 || c.K < 1) {
			return fmt.Errorf("SEPARATION needs S and k >= 1")
		}
		if kind == KindSeparation {
			c.S = sortedSet(append(c.S, *c.U, *c.V))
		}
		c.Coloring = nil
	case KindNote:
	default:
		return fmt.Errorf("unknown kind %q", kind)
	}
	return nil
}

// ClaimID is the content address of a normalized claim. Two agents that find
// the same claim get the same id, so the verifier only checks it once.
func ClaimID(kind string, c *Claim) string {
	b, _ := json.Marshal(struct {
		Kind  string `json:"kind"`
		Claim *Claim `json:"claim"`
	}{kind, c})
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:12])
}

func sortedSet(s []int) []int {
	if len(s) == 0 {
		return s
	}
	out := append([]int(nil), s...)
	sort.Ints(out)
	j := 0
	for i := range out {
		if i == 0 || out[i] != out[i-1] {
			out[j] = out[i]
			j++
		}
	}
	return out[:j]
}
