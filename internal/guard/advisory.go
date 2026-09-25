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
	advisorySourceRead    hint.MarkerKind = "source-read"
	advisoryPrecedent     hint.MarkerKind = "precedent-search"
	advisoryStageClassify hint.MarkerKind = "stage-classify"
	advisoryUnleasedWrite hint.MarkerKind = "unleased-write"
	advisorySkillSource   hint.MarkerKind = "skill-source"
	advisoryRegenSource   hint.MarkerKind = "regen-source"
	advisoryGraphStale    hint.MarkerKind = "graph-stale"
	advisoryGateRepeat    hint.MarkerKind = "gate-repeat"
	advisoryFocus         hint.MarkerKind = "focus"
	advisoryHookWiring    hint.MarkerKind = "hook-wiring"
	advisoryNewFile       hint.MarkerKind = "new-file"
	advisoryLeaseTerminal hint.MarkerKind = "lease-terminal"
	advisoryLeaseInvalid  hint.MarkerKind = "lease-invalid"
	// advisoryLeasedPath is a write into paths a live lease owns, by a caller naming no
	// lease. It was the most served advisory in the 2026-09-24 audit, 8,419 of 16,240, one
	// per edit; it is held per lease, see leasedPathKey.
	advisoryLeasedPath hint.MarkerKind = "leased-path"
	// advisorySplitRun covers two shapes of the same mistake: `magus run` (or `affected`)
	// takes one target and many projects, so the same target run twice on different
	// project sets is usually one call typed twice. On ONE line it is a denyRuleName,
	// like advisoryChainedRun beside it, because a chain worth questioning on sight is
	// worth questioning every time it is typed again. ACROSS two calls it is held to one
	// firing per session (see internal/guard/splitrun.go), since the session already
	// knows and a second reminder of the same standing fact teaches nothing new.
	advisorySplitRun hint.MarkerKind = "split-run"
	// advisorySharedCheckout fires on a SPAWN, which is the one moment the choice between
	// one checkout and two is still free to make.
	advisorySharedCheckout hint.MarkerKind = "shared-checkout"

	// Enrolled late. These five and the three VCS kinds below shipped anonymous, which
	// an empty kind spells as "speak every time": they had no marker, so they repeated in
	// full on every matching call and no verdict could name them. Both halves were the
	// same gap, because the kind IS the name.
	//
	// Each one gets a brief alongside, so enrolling degrades it to a line rather than
	// silencing it. A notice that carries a command earns a repeat at a size nobody has
	// to read around; see Gate.OnceOrBrief.
	advisoryGeneratedWrite hint.MarkerKind = "generated-write"
	advisoryInstalledSkill hint.MarkerKind = "installed-skill"
	advisoryMemoryWrite    hint.MarkerKind = "memory-write"
	advisoryScopeDrift     hint.MarkerKind = "scope-drift"
	advisoryNewSourceDir   hint.MarkerKind = "new-source-dir"
)

// The VCS advisories name themselves without enrolling in the gate above, so they are
// denyRuleName values rather than MarkerKinds: a kind is a marker KEY, and holding these
// is a behavior change none of them asked for. See ShellVerdict.Rule.
//
// denyRuleName is the type despite these not denying. Renaming it to cover both arms
// would touch every rule in the file for a word, and the field it lands in, Rule, already
// reads correctly on either.
const (
	advisoryPushGate        denyRuleName = "push-gate"
	advisoryRevertClassify  denyRuleName = "revert-classify"
	advisoryCheckpointState denyRuleName = "checkpoint-state"
	// advisoryChainedRun names an advisory that was firing anonymously. Unnamed, it was
	// outside every count magus keeps, so the rule this repo applies to its own advice
	// (next.go: uptake is a query, and advice nobody takes gets deleted) could not reach
	// it. MEASURED 2026-09-20: it fired three times in one session on the same shape and
	// was ignored all three, which is a fact worth being able to READ rather than
	// reconstruct from a transcript.
	advisoryChainedRun denyRuleName = "chained-run"
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

// leasedPathKey holds the leased-path advisory once per session per LEASE, hashed for the
// reason advisoryFocusPath is. The subject is the lease, not the file: a second path the
// same lease owns teaches nothing the first did not.
func leasedPathKey(lease string) hint.MarkerKind {
	sum := sha256.Sum256([]byte(lease))
	return advisoryLeasedPath + "-" + hint.MarkerKind(hex.EncodeToString(sum[:6]))
}
