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
	Symbols []ImpactSymbol `json:"symbols,omitempty" yaml:"symbols,omitempty"`
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
