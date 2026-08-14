package types

import "slices"

// Review is the annotated changeset: every changed file with what the workspace already
// knows about it. It is what separates a magus review from a text diff.
//
// The thesis is that a changeset is not a list of files with text changes, it is a set of
// CONSEQUENCES, and a reader's attention is scarce. Alphabetical order spends that attention
// at random and gives a regenerated lockfile the same weight as a signature change twelve
// packages depend on. Everything here exists to let a reader spend attention in consequence
// order instead.
//
// Nothing in here is computed for the review: Role comes from the same declared-output globs
// `magus describe file` reads, Reach and Coverage from the same overlays `magus affected
// --impact` prints. The review is a JOIN over answers the workspace already had.

// ReviewRole repeats FileEntry.Role rather than importing its meaning, because the review's
// use of it is narrower: the only question a reader is asking is whether this file is
// something they must read or something a target will rewrite.
const (
	// ReviewRoleOutput is a declared target output. It is GENERATED, so reviewing its diff is
	// reading a machine's opinion of a change made elsewhere - the source edit is the review.
	ReviewRoleOutput = "output"
	// ReviewRoleSource is a declared source: it feeds cache keys and the affected set.
	ReviewRoleSource = "source"
	// ReviewRoleMaintained is written by magus itself outside any target.
	ReviewRoleMaintained = "maintained"
	// ReviewRoleUnclaimed is declared by nothing and invalidates nothing.
	ReviewRoleUnclaimed = "unclaimed"
)

// ReviewSurface is how far a changed symbol's referents reach, which is the question a
// semver decision actually turns on. It is EVIDENCE, never a verdict: magus reports where a
// symbol is used and lets the reader decide the bump.
//
// It deliberately stops short of claiming a break. Deciding that needs signature
// compatibility, which needs a base-side index magus does not have (the symbol shards
// describe the working tree, not history) and language semantics magus does not model. A
// tool that guessed at it would be wrong in exactly the cases that matter most - an
// unchanged signature with changed behaviour, a widened parameter type - and a
// breaking-change warning nobody trusts is worse than none. The per-language answer belongs
// in a spell op (an apidiff), whose output joins this same review.
const (
	// ReviewSurfaceInternal means every referent lives inside the defining project. A change
	// here cannot break a consumer that the workspace does not also rebuild.
	ReviewSurfaceInternal = "internal"
	// ReviewSurfacePublic means at least one referent lives in ANOTHER project: the symbol is
	// API surface across a boundary the workspace itself draws.
	ReviewSurfacePublic = "public"
	// ReviewSurfaceUnknown means no symbol index covered the file, so the question was not
	// answered. It must never render as "internal" - "we did not look" and "we looked and
	// found nothing" are different facts, and collapsing them is how a signal earns the right
	// to be ignored.
	ReviewSurfaceUnknown = "unknown"
)

// ReviewSymbol is one changed symbol with its exposure.
type ReviewSymbol struct {
	ID    string `json:"id" yaml:"id"`
	Label string `json:"label,omitempty" yaml:"label,omitempty"`
	// RefCount and FileCount are occurrences and distinct referencing files.
	RefCount  int `json:"ref_count"  yaml:"ref_count"`
	FileCount int `json:"file_count" yaml:"file_count"`
	// ExternalProjects are the OTHER projects that reference this symbol, sorted. Non-empty
	// is what makes a file's surface public, and naming them answers the reader's actual next
	// question - who breaks - rather than only how many.
	ExternalProjects []string `json:"external_projects,omitempty" yaml:"external_projects,omitempty"`
	// ExternalFileCount is how many referencing files sit outside the defining project.
	ExternalFileCount int `json:"external_file_count" yaml:"external_file_count"`
	// ModuleAPI reports that this symbol is exported from the MODULE - reachable by a
	// consumer outside the workspace entirely.
	//
	// It is a separate question from ExternalProjects and neither implies the other, which is
	// the whole reason both exist. A symbol can be referenced by no other project and still be
	// public API that a downstream module imports; measured on this repository, every referent
	// of the root package sits in the root project, so cross-project exposure alone reported
	// the published SDK surface as internal. Conflating the two answers the wrong question:
	// "who in this workspace breaks" is not "who in the world breaks".
	ModuleAPI bool `json:"module_api,omitempty" yaml:"module_api,omitempty"`
}

// ReviewFile is one changed file, annotated.
type ReviewFile struct {
	Path string `json:"path" yaml:"path"`
	// Project is the owning project, empty when no project directory contains the file.
	Project string `json:"project,omitempty" yaml:"project,omitempty"`
	// Role is one of the ReviewRole constants. It is the single most useful fact about a
	// changed file, because it answers "must I read this" before any of the rest.
	Role string `json:"role" yaml:"role"`
	// Hint is magus's own sentence about the role, reused verbatim from describe file so the
	// console and the CLI never drift into two explanations of one classification.
	Hint string `json:"hint,omitempty" yaml:"hint,omitempty"`
	// Coverage is the file's observed coverage, nil when none was measured. Nil is DISTINCT
	// from zero: "no coverage run has happened" must not render as "this code is untested".
	Coverage *ImpactCoverage `json:"coverage,omitempty" yaml:"coverage,omitempty"`
	// Symbols are the changed symbols this file defines, each carrying how widely it is
	// referenced. Empty when no symbol index covers the file.
	Symbols []ReviewSymbol `json:"symbols,omitempty" yaml:"symbols,omitempty"`
	// Surface is one of the ReviewSurface constants: whether any changed symbol here is
	// referenced from another project. It is the semver-relevant fact, and it is evidence
	// rather than a verdict - see ReviewSurface.
	Surface string `json:"surface" yaml:"surface"`
	// Reach is the widest FileCount among Symbols: how many files reference the most-referenced
	// thing this file changed. It is the ranking key, and it is deliberately a COUNT OF FILES
	// rather than of references - one file calling a function forty times is one file that
	// breaks, and ranking by reference count would put a hot loop above a widely-used API.
	Reach int `json:"reach" yaml:"reach"`
}

// Generated reports whether reviewing this file's diff is reading generated output.
func (f ReviewFile) Generated() bool { return f.Role == ReviewRoleOutput }

// Review is the whole annotated changeset.
type Review struct {
	// Base is the ref the diff was taken against, or "working" for the uncommitted tree.
	Base string `json:"base" yaml:"base"`
	// Files carries one entry per changed path, in the order magus recommends READING them:
	// consequence first. See SortForReview.
	Files []ReviewFile `json:"files,omitempty" yaml:"files,omitempty"`
	// SeedProjects are the projects a changed file lands in directly - the ones the author
	// actually edited, as opposed to the ones that merely rebuild.
	SeedProjects []string `json:"seed_projects,omitempty" yaml:"seed_projects,omitempty"`
	// AffectedProjects is the full reverse closure. The gap between its length and
	// SeedProjects' is the whole "why is docs in my build" question, so both ship.
	AffectedProjects []ImpactProject `json:"affected_projects,omitempty" yaml:"affected_projects,omitempty"`
	// Notes are magus-authored caveats about what could NOT be computed (no symbol index, no
	// coverage run). They are surfaced rather than swallowed: a reader who sees no reach
	// numbers must be able to tell "nothing depends on this" from "nothing was measured".
	Notes []string `json:"notes,omitempty" yaml:"notes,omitempty"`
}

// GeneratedCount reports how many files are declared outputs - the ones a reader can fold
// away. It is the headline of the noise-collapse affordance.
func (r Review) GeneratedCount() int {
	n := 0
	for _, f := range r.Files {
		if f.Generated() {
			n++
		}
	}
	return n
}

// SortForReview orders Files into the sequence magus recommends reading them in. It sorts in
// place and is the one definition of "review order", so the console, the CLI, and a Buzz
// advisor writing a pull-request comment all agree on what to read first.
//
// The rule, in order:
//
//  1. Generated output goes LAST, always, whatever its reach. Its diff is a machine's
//     restatement of a change made somewhere else, so reading it before the source that
//     caused it is reading the answer before the question.
//  2. Then widest reach first: the file whose changed symbols are referenced from the most
//     other files is the one most able to break something.
//  3. Then unclaimed and maintained files after ordinary sources - they invalidate no cache
//     key and affect no target, so nothing downstream turns on them.
//  4. Path, last, purely so the order is deterministic. It is a TIEBREAK and never a
//     ranking: alphabetical order is what this exists to replace.
func (r Review) SortForReview() {
	rank := func(f ReviewFile) int {
		switch f.Role {
		case ReviewRoleOutput:
			return 3
		case ReviewRoleUnclaimed, ReviewRoleMaintained:
			return 2
		default:
			return 1
		}
	}
	slices.SortFunc(r.Files, func(a, b ReviewFile) int {
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra - rb
		}
		if a.Reach != b.Reach {
			return b.Reach - a.Reach // widest reach first
		}
		switch {
		case a.Path < b.Path:
			return -1
		case a.Path > b.Path:
			return 1
		}
		return 0
	})
}
