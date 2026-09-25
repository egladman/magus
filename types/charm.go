package types

import (
	"context"
	"slices"
)

// Charms are named, shared execution modifiers that change how a target runs
// without changing which target or project. They are an additive, unordered set
// carried on the context: multiple charms stack and coexist (none overrides
// another), each spell reacts only to the charms it understands via HasCharm and
// ignores the rest, and duplicates are insignificant since membership is all
// that matters. "rw" is a built-in charm (mutate in place), activated like
// any other via a "target:rw" suffix. Charms are meant to be reused across
// targets, not defined per-target. See docs/charms.md for the full design intent.
type charmsContextKey struct{}

// CharmReadWrite is a reserved built-in charm: the read→write toggle that flips
// check-only targets (format, lint, generate) to mutate in place. Magusfiles read
// it via has_charm("rw"); its per-tool effect is declared by each spell's charms
// table. The name is reserved so it is recognized everywhere — the typo guard skips
// it (see undeclaredCharms) and the read-only ci gate strips it (see RunCI).
const CharmReadWrite = "rw"

// CharmCD is a reserved built-in charm: the opt-in continuous-delivery toggle a
// target's body reads via has_charm to publish its artifact (push an image, upload
// an archive). It pairs with the ci target — magus run ci:cd. Reserved so it is
// recognized everywhere and the typo guard skips it (see undeclaredCharms). It is
// the additive opposite of write — it adds a deliver side effect rather than
// flipping check→mutate — and the ci gate does not strip it, so a ci run can still
// deliver.
const CharmCD = "cd"

// CharmGHA is a reserved built-in charm: opt into GitHub Actions output. Spells
// that drive a tool with a GitHub-annotation output mode declare it in their charms
// table to swap the tool's reporter to that format (so failures surface as inline
// `::error::` workflow annotations on the PR). Set it in CI via `magus run ci:gha`.
// Reserved so the typo guard skips it everywhere (a ci run fans out to tools that
// don't support it, where it is simply a no-op — see undeclaredCharms); the ci gate
// does not strip it (unlike rw), so the annotations survive into ci.
const CharmGHA = "gha"

// CharmUpdate is a reserved built-in charm: the grant to move a pinned copy of upstream
// state forward to whatever upstream serves today. Re-resolving a lockfile (go mod tidy)
// and refreshing a scanner's vulnerability database are the same grant under one name,
// because they answer the same question: may this run replace what it pinned. Deliberately
// not part of rw: rw regenerates derived output from this tree and so is reproducible,
// while an update reads a registry or a feed and yields different bytes on different days.
// Folded into rw, a workspace with default_charms: [rw] would re-resolve dependencies
// during an unrelated build. Stripped from ci alongside rw (see RunCI).
const CharmUpdate = "update"

// CharmExtended is a reserved built-in charm: the extended test suite, which adds to the
// default one the tests that need more of the host than every gate should require (a C
// toolchain, a docker daemon, more time or network). A test target keeps its default tier
// and reads has_charm("extended") to need the rest; it pairs with test and ci (magus run
// ci:extended). The ci gate does not strip it, and a target it pulls in fails on a missing
// tool rather than skipping, since asking for the extended suite asked for that test.
//
// Chosen over full, which six persona consults read as a cold, uncached rebuild of every
// project, and over test kinds (e2e, integration), which name what a test is rather than
// what it needs, so the same tier would need several charms.
const CharmExtended = "extended"

// reservedCharms are the built-in charm names magus recognizes without any target
// declaring them. Listed once here so the typo guard (IsReservedCharm) and the
// doctor name-collision check (ReservedCharms) cannot drift. The entries are
// already in canonical (normalized) form.
var reservedCharms = []string{CharmReadWrite, CharmCD, CharmGHA, CharmUpdate, CharmExtended}

// renamedCharms maps a retired built-in charm name to the name that replaced it. Keys
// are canonical (normalized) form.
//
// compat(until: no supported workspace spells a charm relock): observe with a search for
// `:relock` in magusfiles and `default_charms`; then drop this, RenamedCharmError and its
// call in checkUndeclaredCharms, keeping the MGS6002 page.
var renamedCharms = map[string]string{
	"relock": CharmUpdate,
}

// RenamedCharmError returns a CharmRenamed error when name is a retired built-in charm,
// and nil otherwise. Callers ask only about a charm no selected target declares: a
// target may still declare a charm of its own under a retired name.
func RenamedCharmError(name string) error {
	to, ok := renamedCharms[NormalizeCharm(name)]
	if !ok {
		return nil
	}
	return DiagnosticErrorf(CharmRenamed,
		"charm %q was renamed %q in v0.5.0 and nothing answers to the old name; spell it `:%s`", name, to, to)
}

// ReservedCharms returns magus's built-in charm names as a fresh slice.
func ReservedCharms() []string { return slices.Clone(reservedCharms) }

// IsReservedCharm reports whether name — in any casing or separator form — is one
// of magus's reserved built-in charms.
func IsReservedCharm(name string) bool {
	return slices.Contains(reservedCharms, NormalizeCharm(name))
}

// NormalizeCharm canonicalizes a charm name with Normalize's case and separator
// folding. It stays a distinct name from Normalize because every charm path routes
// through it, so a spell arm, a magusfile and the ci strip compare one spelling and
// a future alias has one place to land.
func NormalizeCharm(name string) string { return Normalize(name) }

// ReservedCharmDoc returns a one-line description of a reserved built-in charm, or
// "" for a name that is not reserved. It is the single source `magus describe charm`
// reads, so the built-in summaries cannot drift from the reserved set.
func ReservedCharmDoc(name string) string {
	switch NormalizeCharm(name) {
	case CharmReadWrite:
		return "mutate in place: flip check-only targets (format, lint, generate) to write; stripped from ci"
	case CharmCD:
		return "continuous-delivery: a target reads it to publish its artifact; survives into ci"
	case CharmGHA:
		return "GitHub Actions output: swap a tool's reporter to inline workflow annotations; survives into ci"
	case CharmUpdate:
		return "move pinned upstream state forward (a lockfile, a scanner database) instead of verifying it; stripped from ci"
	case CharmExtended:
		return "the extended test suite: add the tests that need more of the host (a C toolchain, docker) than the default gate; survives into ci"
	default:
		return ""
	}
}

// WithCharms returns a context carrying the active execution charms, normalized
// (see NormalizeCharm) so the stored set is canonical and HasCharm only has
// to normalize the query. An empty set leaves the context unchanged, so it never
// clobbers existing charms. Callers pass the full accumulated set (e.g. the
// charms in a "name:a,b" suffix).
func WithCharms(ctx context.Context, charms []string) context.Context {
	if len(charms) == 0 {
		return ctx
	}
	normalized := make([]string, len(charms))
	for i, c := range charms {
		normalized[i] = NormalizeCharm(c)
	}
	return context.WithValue(ctx, charmsContextKey{}, normalized)
}

// CharmsFromContext returns the active execution charms, or nil if none were set.
func CharmsFromContext(ctx context.Context) []string {
	if v, ok := ctx.Value(charmsContextKey{}).([]string); ok {
		return v
	}
	return nil
}

// HasCharm reports whether charm is among the active execution charms.
// This membership test is how a spell opts into a charm's behavior; charms it
// does not test for are simply ignored. The query is normalized and the active
// set is already canonical (WithCharms normalizes on store), so a spell that
// tests has_charm("noCache") matches a "target:no-cache" suffix regardless of
// casing or separator.
func HasCharm(ctx context.Context, charm string) bool {
	want := NormalizeCharm(charm)
	for _, c := range CharmsFromContext(ctx) {
		if c == want {
			return true
		}
	}
	return false
}
