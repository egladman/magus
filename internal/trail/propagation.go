package trail

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/egladman/magus/types"
)

// The environment channels a spawning tool uses to say what it is and where this process sits
// under it. TRACEPARENT and BAGGAGE are the W3C Trace Context and Baggage headers, carried in
// the environment under the OpenTelemetry convention for propagating across a process boundary
// that has no HTTP request to hang a header on.
//
// magus PARSES these two and emits nothing: no SDK, no exporter, no spans on a wire. Adopting
// the grammars is what makes the ancestry readable by tools that already exist, which is the
// whole reason not to invent a format.
//
// TRACESTATE is deliberately not read. It is vendor state for a tracing system to FORWARD,
// magus forwards nothing, and a recorded value nothing consumes is a field to keep true for no
// reader.
const (
	EnvTraceparent = "TRACEPARENT"
	EnvBaggage     = "BAGGAGE"
)

// The baggage members magus reads. Namespaced because BAGGAGE is a shared channel: every other
// member belongs to somebody else and is left alone.
const (
	// BaggageLease names the ledger lease the process is ACTING as.
	BaggageLease = "magus.lease"
	// BaggageSpawner names the tool or session that spawned it, as a label for a person to
	// read. It is free text and a claim; nothing resolves it and no verdict reads it.
	BaggageSpawner = "magus.spawner"
)

// MaxSpawnerLen bounds the spawner label. It rides every session record, so an unbounded label
// is a cost every later read of the repository pays; 128 is past any name a person can use.
const MaxSpawnerLen = 128

// Spawn is what the environment CLAIMED about the process that started this one, recorded
// verbatim and corroborated by nothing.
//
// Trust tier, stated once because every field shares it: the worker's own claim about itself,
// arriving over a channel any local process may set. NO verdict may key on any of it. The one
// grading that reads a field here
// takes Lease to the ledger boundary check, and that check fails open (see
// cmd/magus/guard_write.go). ParentSpanID is read in one more place and for teaching only:
// adviseUnleasedWorker turns silence into an advisory when a process claiming a spawner writes
// while no lease exists to grade it, which can never deny and never changes a verdict another
// rule reached. The human is never a claim: a run carrying no trace context IS a
// person, and that root is inferred from the transport rather than asserted by anyone.
type Spawn struct {
	// TraceID and ParentSpanID are the trace this process was spawned into and the span that
	// spawned it, from TRACEPARENT: 32 and 16 lowercase hex characters, or "" when nothing was
	// claimed or the value did not parse. Flags is the trace-flags octet, kept because it is
	// what a later exporter would need and dropping it would make the recorded traceparent
	// unreconstructable.
	TraceID      string
	ParentSpanID string
	Flags        string
	// Lease is the ledger lease the process acts as, from the magus.lease
	// baggage member, percent-decoded and validated by [types.ValidLeaseID] - the same
	// rule every lease channel shares, and what keeps the trail's redaction exemption
	// honest.
	Lease string
	// Spawner is the magus.spawner baggage member: a label for whoever spawned this, for a
	// person reading `magus session ls`. Percent-decoded, clamped to [MaxSpawnerLen].
	Spawner string
}

// traceparentNoteOnce and baggageNoteOnce hold each malformed-value note to one per process. The
// environment does not change under a running worker, so the same bad value would otherwise be
// reported once per event and drown the run in one repeated fact.
var (
	traceparentNoteOnce sync.Once
	baggageNoteOnce     sync.Once
)

// SpawnFromEnv reads the propagation channels this process was launched with. Every field is
// independently optional: a process started by a person carries none of them, which is the
// designed outcome and never an error.
//
// A malformed value is DROPPED with a one-time note rather than recorded. Dropping is what keeps
// the redaction exemption honest for the lease id; the note is what keeps a typo'd
// environment from looking like a fleet that simply never attributed anything.
func SpawnFromEnv() Spawn {
	spawn := parseTraceparent(strings.TrimSpace(os.Getenv(EnvTraceparent)))
	spawn.Lease, spawn.Spawner = parseBaggage(strings.TrimSpace(os.Getenv(EnvBaggage)))
	return spawn
}

// NewSpanID mints this process's own span id: 16 lowercase hex characters, 8 bytes of
// crypto/rand. It is the one identity here magus asserts rather than records, which is why it is
// minted and never read from the environment.
func NewSpanID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read does not fail on any platform magus runs on; a session with no span
		// id is still a session, and refusing to start one over an id nothing gates would be
		// the worse outcome.
		return ""
	}
	return hex.EncodeToString(b[:])
}

// traceparentVersion is the only version this parses. A future version is a value magus cannot
// claim to understand, and the W3C forward-compatibility rule (parse what you know of a higher
// version) buys nothing here: magus reads two fields and forwards none.
const traceparentVersion = "00"

// parseTraceparent reads `00-<32 hex trace-id>-<16 hex parent-span-id>-<2 hex flags>`. An
// all-zero trace or span id is invalid per the spec - it is the wire's way of spelling "absent" -
// and so is any hex outside lowercase, which the spec fixes so two encodings of one id cannot
// exist.
func parseTraceparent(v string) Spawn {
	if v == "" {
		return Spawn{}
	}
	parts := strings.Split(v, "-")
	ok := len(parts) == 4 &&
		parts[0] == traceparentVersion &&
		validTraceID(parts[1], 32) &&
		validTraceID(parts[2], 16) &&
		lowerHex(parts[3], 2)
	if !ok {
		traceparentNoteOnce.Do(func() {
			// The value itself is not logged: it failed the shape that makes it safe to carry
			// unredacted, so it is the one string here that could be anything at all.
			slog.WarnContext(context.Background(),
				"magus: ignoring "+EnvTraceparent+" and recording no trace for this process: a W3C traceparent is 00-<32 hex trace id>-<16 hex span id>-<2 hex flags>, lowercase, with neither id all zeros",
				slog.Int("length", len(v)))
		})
		return Spawn{}
	}
	return Spawn{TraceID: parts[1], ParentSpanID: parts[2], Flags: parts[3]}
}

// validTraceID is a lowercase-hex id of exactly n characters that is not all zeros.
func validTraceID(s string, n int) bool {
	return lowerHex(s, n) && strings.Trim(s, "0") != ""
}

func lowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

// maxBaggageLen bounds the value this will walk. The W3C limit is 8192 bytes over all members;
// anything past it is not baggage magus is going to salvage two keys out of.
const maxBaggageLen = 8192

// parseBaggage extracts the two magus members from a W3C baggage list: comma-separated
// `key=value` members, each optionally carrying `;`-delimited properties that are ignored, with
// percent-encoded values. Every other member is left untouched - baggage is a shared channel,
// and magus is one tenant of it.
//
// A member magus cannot read is skipped rather than failing the list: a value this process did
// not write must not be able to cost it its own attribution.
func parseBaggage(v string) (lease, spawner string) {
	if v == "" {
		return "", ""
	}
	if len(v) > maxBaggageLen {
		baggageNoteOnce.Do(func() {
			slog.WarnContext(context.Background(),
				"magus: ignoring "+EnvBaggage+" and recording no lease for this process: a W3C baggage list is a bounded set of comma-separated key=value members",
				slog.Int("length", len(v)),
				slog.Int("max_length", maxBaggageLen))
		})
		return "", ""
	}
	for _, member := range strings.Split(v, ",") {
		if i := strings.Index(member, ";"); i >= 0 {
			member = member[:i] // properties describe the member; magus reads none of them
		}
		key, value, found := strings.Cut(member, "=")
		if !found {
			continue
		}
		// PathUnescape, not QueryUnescape: baggage values are percent-encoded per RFC 3986, so
		// a literal "+" is a plus and not a space.
		decoded, err := url.PathUnescape(strings.TrimSpace(value))
		if err != nil {
			continue
		}
		switch strings.TrimSpace(key) {
		case BaggageLease:
			lease = validLease(strings.TrimSpace(decoded))
		case BaggageSpawner:
			spawner = clampSpawner(strings.TrimSpace(decoded))
		}
	}
	return lease, spawner
}

// validLease returns id when it may be stamped, and "" plus a one-time note when it cannot.
func validLease(id string) string {
	if id == "" {
		return ""
	}
	if !types.ValidLeaseID(id) {
		baggageNoteOnce.Do(func() {
			slog.WarnContext(context.Background(),
				"magus: ignoring the lease in "+EnvBaggage+" and recording no lease for this process: a lease id is letters, digits and -_./: only, and never empty",
				slog.Int("length", len(id)),
				slog.Int("max_length", types.MaxLeaseIDLen))
		})
		return ""
	}
	return id
}

// clampSpawner bounds a label, cutting on a rune boundary so the stored record stays valid
// UTF-8. Not validated further: it is a claim meant for a person to read, and rejecting it for
// its shape would drop attribution rather than improve it.
func clampSpawner(s string) string {
	if len(s) <= MaxSpawnerLen {
		return s
	}
	cut := MaxSpawnerLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
