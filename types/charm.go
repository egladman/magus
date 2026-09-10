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

// CharmUpdateAlias is the spelling CharmUpdate had before a second op claimed the grant.
//
// compat(until: no workspace magus supports still spells this charm "relock"): the charm
// was named relock while go mod tidy was the only op that claimed it. NormalizeCharm
// resolves it to CharmUpdate, so `target:relock` keeps running for one release and every
// consumer downstream (spell charm arms, the ci strip, the typo guard) sees only update.
// Observing that it is safe to drop: a release carrying the deprecation notice
// hintCanonicalSpelling prints has shipped, and `magus query charm=relock` over the
// workspaces you support returns nothing. Delete this const and the NormalizeCharm branch
// together; the spell arms and docs already say update.
const CharmUpdateAlias = "relock"

// reservedCharms are the built-in charm names magus recognizes without any target
// declaring them. Listed once here so the typo guard (IsReservedCharm) and the
// doctor name-collision check (ReservedCharms) cannot drift. The entries are
// already in canonical (normalized) form.
var reservedCharms = []string{CharmReadWrite, CharmCD, CharmGHA, CharmUpdate}

// ReservedCharms returns magus's built-in charm names as a fresh slice.
func ReservedCharms() []string { return slices.Clone(reservedCharms) }

// IsReservedCharm reports whether name — in any casing or separator form — is one
// of magus's reserved built-in charms.
func IsReservedCharm(name string) bool {
	return slices.Contains(reservedCharms, NormalizeCharm(name))
}

// NormalizeCharm canonicalizes a charm name: Normalize's case and separator folding,
// plus the one compatibility alias magus still answers to. Every path that reads a charm
// name goes through it, so a run that says relock reaches a spell, a magusfile and the ci
// strip as update and none of them has to know the alias exists.
func NormalizeCharm(name string) string {
	if n := Normalize(name); n != CharmUpdateAlias {
		return n
	}
	return CharmUpdate
}

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
