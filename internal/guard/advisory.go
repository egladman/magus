package guard

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/egladman/magus/internal/hint"
)

// The guard's own advisory-tier notices: the once-per-session marker mechanics live in
// internal/hint (cmd/magus/next.go needs the same gate and cannot reach an unexported
// guard type), but the kinds enrolled below are this package's vocabulary, not hint's.

const (
	advisoryStaleBinary   hint.MarkerKind = "stale-binary"
	advisoryCodeSearch    hint.MarkerKind = "code-search"
	advisoryDocSearch     hint.MarkerKind = "doc-search"
	advisoryPrecedent     hint.MarkerKind = "precedent-search"
	advisoryStageClassify hint.MarkerKind = "stage-classify"
	advisoryUnleasedWrite hint.MarkerKind = "unleased-write"
	advisorySkillSource   hint.MarkerKind = "skill-source"
	advisoryRegenSource   hint.MarkerKind = "regen-source"
	advisoryGraphStale    hint.MarkerKind = "graph-stale"
	advisoryGateRepeat    hint.MarkerKind = "gate-repeat"
	advisoryFocus         hint.MarkerKind = "focus"
	advisoryHookWiring    hint.MarkerKind = "hook-wiring"
	advisoryLeaseTerminal hint.MarkerKind = "lease-terminal"
	advisoryLeaseInvalid  hint.MarkerKind = "lease-invalid"
)

// advisoryFocusPath keys a marker on the PATH as well as on the kind, so a session
// that reads one out-of-focus file twice is told once.
//
// The focus rule is the one advisory whose subject changes call to call: every other
// enrolled kind reports a standing fact, so one firing per session says all of it,
// while a second out-of-focus path is a second fact the reader has not been told.
// Two markers, then: this one silences the repeat of a path, and advisoryFocus above
// spends the session's one full explanation, leaving the brief line for the rest.
//
// Hashed rather than sanitized, for the reason hint.MarkerPath hashes the session id: a
// path may hold separators, and a filename assembled from one is a filename the
// input picked.
func advisoryFocusPath(rel string) hint.MarkerKind {
	sum := sha256.Sum256([]byte(rel))
	return advisoryFocus + "-" + hint.MarkerKind(hex.EncodeToString(sum[:6]))
}
