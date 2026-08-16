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

// DiffRole repeats FileEntry.Role rather than importing its meaning, because the review's
// use of it is narrower: the only question a reader is asking is whether this file is
// something they must read or something a target will rewrite.
const (
	// DiffRoleOutput is a declared target output. It is GENERATED, so reviewing its diff is
	// reading a machine's opinion of a change made elsewhere - the source edit is the review.
	DiffRoleOutput = "output"
	// DiffRoleSource is a declared source: it feeds cache keys and the affected set.
	DiffRoleSource = "source"
	// DiffRoleMaintained is written by magus itself outside any target.
	DiffRoleMaintained = "maintained"
	// DiffRoleUnclaimed is declared by nothing and invalidates nothing.
	DiffRoleUnclaimed = "unclaimed"
)

// DiffSurface is how far a changed symbol's referents reach, which is the question a
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
	// DiffSurfaceInternal means every referent lives inside the defining project. A change
	// here cannot break a consumer that the workspace does not also rebuild.
	DiffSurfaceInternal = "internal"
	// DiffSurfacePublic means at least one referent lives in ANOTHER project: the symbol is
	// API surface across a boundary the workspace itself draws.
	DiffSurfacePublic = "public"
	// DiffSurfaceUnknown means no symbol index covered the file, so the question was not
	// answered. It must never render as "internal" - "we did not look" and "we looked and
	// found nothing" are different facts, and collapsing them is how a signal earns the right
	// to be ignored.
	DiffSurfaceUnknown = "unknown"
)

// DiffSymbol is one changed symbol with its exposure.
type DiffSymbol struct {
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

// DiffChurn is how often this file has been changing, and whether that is accelerating.
//
// It answers a question the diff itself cannot: not "is this change correct" but "is this file
// being rewritten over and over". A file edited repeatedly is frequently a design problem
// wearing a series of small fixes - the circle you notice only in hindsight, after the fifth
// visit. Surfacing it AT review time is the whole point, because that is the one moment
// somebody is already looking at the file and could still decide to fix the cause instead.
//
// Every field is a measurement over a bounded commit window, not a judgment. Rank is the
// useful one to render: "third-hottest file in the workspace" means something to a reader in
// a way a raw score never will.
type DiffChurn struct {
	// Commits is how many commits in the window touched this file.
	Commits int `json:"commits" yaml:"commits"`
	// Authors is how many distinct people did. One author on a hot file is a bus-factor
	// problem; many on a hot file is a coordination one. Both are worth knowing, neither is
	// worth magus deciding.
	Authors int `json:"authors,omitempty" yaml:"authors,omitempty"`
	// Score is commits x complexity - the hotspot ranking's own metric.
	Score int `json:"score" yaml:"score"`
	// Rank is this file's 1-based position in the workspace's hotspot ranking; 0 when it did
	// not rank at all, which is the common and unremarkable case.
	Rank int `json:"rank,omitempty" yaml:"rank,omitempty"`
	// ProjectTrend is the owning project's churn delta across the window's two halves.
	// Positive is accelerating. It is PROJECT level because that is the granularity the trend
	// lens measures; a file-level trend would be inventing precision the data does not have.
	ProjectTrend int `json:"project_trend,omitempty" yaml:"project_trend,omitempty"`
}

// Rising reports whether this file is both hot and getting hotter - the combination worth
// interrupting a reader for. Either signal alone is ordinary: plenty of files are hot because
// they are big, and plenty of projects are accelerating for good reasons.
func (c DiffChurn) Rising() bool { return c.NotableRank() && c.ProjectTrend > 0 }

// NotableRankCutoff is how far down the hotspot ranking is still worth SHOWING.
//
// The rank itself is honest at any depth; rendering it is not. "Hotspot #1278" tells a reader
// nothing except that a ranking exists, and a field that is usually meaningless is a field
// they learn to skip - which then costs them the one time it says #3. The data stays whole and
// the display is selective, the same trade the guard makes by explaining only what it is sure
// of.
// It is EXPORTED so a renderer can state the rule rather than leaving a reader to infer it.
// An unexplained cutoff makes a missing rank ambiguous - outside the top N, or never computed?
// - and the two have opposite implications for how much of the ranking to trust.
const NotableRankCutoff = 50

// NotableRank reports whether this file ranks high enough for its position to be worth
// showing. A file outside the cutoff still reports its commit count, which is the part that
// remains meaningful on its own - and which is what tells a reader that churn WAS measured
// here, so a missing rank means "outside the top NotableRankCutoff" rather than "not measured".
func (c DiffChurn) NotableRank() bool { return c.Rank > 0 && c.Rank <= NotableRankCutoff }

// DiffTouch is one agent session's contact with a changed file: that it wrote the file, and
// what it was looking at immediately before.
//
// This is the part of a review no forge can produce. A guard hook observes every path an agent
// reaches, so magus can answer "what was it reading when it decided to write this" - the
// closest thing to the author's reasoning that any tool can recover without asking them. A
// diff shows what changed; this shows what the change was made in response to.
//
// Transcript is a POINTER and magus never opens it. The trail stays a record of paths and
// timings, and a reader who wants what was actually said opens the host's own log themselves -
// which is what lets a whole session's reach be carried cheaply while the expensive and
// sensitive detail stays where the host already put it.
type DiffTouch struct {
	Host       string   `json:"host,omitempty"       yaml:"host,omitempty"`
	Session    string   `json:"session,omitempty"    yaml:"session,omitempty"`
	Transcript string   `json:"transcript,omitempty" yaml:"transcript,omitempty"`
	Read       []string `json:"read,omitempty"       yaml:"read,omitempty"`
	// Ran are the PROGRAMS the session ran, never their arguments - see trail.Touch.Ran for
	// the leak that shape exists to prevent. This payload is served to every MCP client.
	Ran []string `json:"ran,omitempty" yaml:"ran,omitempty"`
}

// DiffFile is one changed file, annotated.
type DiffFile struct {
	Path string `json:"path" yaml:"path"`
	// Project is the owning project, empty when no project directory contains the file.
	Project string `json:"project,omitempty" yaml:"project,omitempty"`
	// Role is one of the DiffRole constants. It is the single most useful fact about a
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
	Symbols []DiffSymbol `json:"symbols,omitempty" yaml:"symbols,omitempty"`
	// Surface is one of the DiffSurface constants: whether any changed symbol here is
	// referenced from another project. It is the semver-relevant fact, and it is evidence
	// rather than a verdict - see DiffSurface.
	Surface string `json:"surface" yaml:"surface"`
	// Touches are the agent sessions that wrote this file and what they had READ first.
	// Empty when no guard hook is wired, which is the common case and not a fault.
	Touches []DiffTouch `json:"touches,omitempty" yaml:"touches,omitempty"`
	// Churn is how often this file has been changing, nil when no history lens was attached.
	// Nil is DISTINCT from zero: "nobody measured" and "this file is quiet" are different
	// facts, and a review that renders the first as the second is lying quietly.
	Churn *DiffChurn `json:"churn,omitempty" yaml:"churn,omitempty"`
	// NoHistory reports that the history lens RAN and found this file in none of the commits
	// it walked - a file added in this change, or one untouched for the whole window.
	//
	// It is a ranking signal because absence of history is not the same as absence of risk,
	// and without it the ordering conflates the two. Every other annotation here is derived
	// from a file's past, so a brand-new file collects no churn, no hotspot rank, no authors
	// and no coverage, and sinks to the bottom on the strength of having no evidence. The one
	// file in a real changeset whose tests did not compile was exactly that file. Nothing has
	// exercised this code and nobody has reviewed it before, which is a reason to read it
	// sooner rather than later.
	//
	// False when the lens did not run at all: that is "nobody looked", and it must not render
	// as "this file has history".
	NoHistory bool `json:"no_history,omitempty" yaml:"no_history,omitempty"`
	// Reach is the widest FileCount among Symbols: how many files reference the most-referenced
	// thing this file changed. It is the ranking key, and it is deliberately a COUNT OF FILES
	// rather than of references - one file calling a function forty times is one file that
	// breaks, and ranking by reference count would put a hot loop above a widely-used API.
	//
	// NIL when no symbol index was loaded, and nil is DISTINCT from zero for the same reason
	// Coverage and Churn are: "nothing references this" and "nobody looked" are different
	// facts. As a plain int an unindexed workspace serves `reach: 0` on every file - a
	// valid-looking number that a fleet dashboard reads as "no change touches widely used
	// code". DiffSurfaceUnknown already refuses that collapse; the pointer is the same refusal
	// applied to the field the ordering actually turns on.
	Reach *int `json:"reach" yaml:"reach"`
}

// ReachOr returns the reach, or def when it was not measured. For rendering and comparison
// only - never use it to decide whether reach is KNOWN, which is what the nil is for.
func (f DiffFile) ReachOr(def int) int {
	if f.Reach == nil {
		return def
	}
	return *f.Reach
}

// Generated reports whether reviewing this file's diff is reading generated output.
func (f DiffFile) Generated() bool { return f.Role == DiffRoleOutput }

// Diff is the whole annotated changeset.
type Diff struct {
	// Base is the ref the diff was taken against, or "working" for the uncommitted tree.
	Base string `json:"base" yaml:"base"`
	// Files carries one entry per changed path, in the order magus recommends READING them:
	// consequence first. See SortForReading.
	Files []DiffFile `json:"files,omitempty" yaml:"files,omitempty"`
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

// DiffAuthor says which kind of client produced a comment or a suggestion.
//
// It is STAMPED BY THE DAEMON from the transport the write arrived on, and never read from
// the payload. That is the whole integrity of a paired review: an agent holds an MCP session
// and a person holds a console tab, the daemon can tell them apart, and so an agent cannot
// post as the person. The notes store settled the same question the same way - "a
// self-attested author is forgeable by whatever wrote the file" - and this is that reasoning
// applied to a store an agent IS allowed to write.
type DiffAuthor string

const (
	// DiffAuthorHuman is a write from the console or an interactive CLI.
	DiffAuthorHuman DiffAuthor = "human"
	// DiffAuthorAgent is a write from the MCP surface.
	DiffAuthorAgent DiffAuthor = "agent"
)

// DiffCursor is where a client is looking: a file and, within it, a hunk.
//
// The human's cursor is the one that matters and the one an agent READS. An agent has no
// cursor of its own, on purpose - see DiffSuggestion for why it cannot move this one.
type DiffCursor struct {
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
	// Hunk is the 0-based index of the hunk within the file, or -1 for the file heading.
	Hunk int `json:"hunk" yaml:"hunk"`
}

// DiffComment is one remark attached to a hunk.
//
// Distinct from a note, and deliberately not stored with one. A note is durable workspace
// knowledge whose only provenance is the person who wrote it; a comment is addressed to one
// author about one change and is dead once the change lands. Folding them together would
// either poison the notes store with ephemeral chatter or force review through an editor and
// a commit, which nobody will do. A comment worth keeping graduates into a note by hand,
// which re-attributes it to a person and a commit.
type DiffComment struct {
	ID     string     `json:"id" yaml:"id"`
	Path   string     `json:"path" yaml:"path"`
	Hunk   int        `json:"hunk" yaml:"hunk"`
	Author DiffAuthor `json:"author" yaml:"author"`
	// AgentName is the opaque host label an MCP client passed, empty for a human. Attribution
	// only: nothing branches on it, matching the hook's treatment of the same field.
	AgentName string `json:"agent_name,omitempty" yaml:"agent_name,omitempty"`
	Body      string `json:"body" yaml:"body"`
	// Anchor is the hunk's content digest at the time of writing, so the comment can report
	// that the code under it has since changed rather than silently pointing at moved text.
	Anchor   string `json:"anchor,omitempty" yaml:"anchor,omitempty"`
	Resolved bool   `json:"resolved" yaml:"resolved"`
}

// DiffSuggestion is an agent asking for the human's attention somewhere.
//
// It is a PROPOSAL and never an action, which is the load-bearing decision in the whole
// paired-review design. A tool that lets an agent move the human's viewport is an agent that
// yanks the screen around mid-read; the reviewer stops trusting their own scroll position and
// the tool starts feeling possessed. So the agent explains and the human navigates: a
// suggestion renders as a peripheral affordance the human accepts with one key, and ignoring
// it costs nothing.
//
// This is the review-surface reading of the guard's own rule - deny only what cannot be
// undone, explain everything else. Yanking a viewport cannot be undone, because the reader's
// place in the diff was in their head.
type DiffSuggestion struct {
	ID        string `json:"id" yaml:"id"`
	Path      string `json:"path" yaml:"path"`
	Hunk      int    `json:"hunk" yaml:"hunk"`
	AgentName string `json:"agent_name,omitempty" yaml:"agent_name,omitempty"`
	// Reason is the one line shown beside the affordance. It has to earn the interruption.
	Reason string `json:"reason" yaml:"reason"`
	// Accepted records that the human took it, so the agent can tell "not yet seen" from
	// "seen and declined" instead of repeating itself.
	Accepted bool `json:"accepted" yaml:"accepted"`
	Declined bool `json:"declined" yaml:"declined"`
}

// DiffSession is the shared object a console tab, an MCP agent, and the CLI all read.
//
// One object rather than three implementations: the daemon already multiplexes those three
// transports over one workspace, so a review they each rebuilt privately would be three
// diverging opinions of the same changeset. Sharing it is what makes pairing real - the agent
// can see where the human is and be useful about it rather than narrating blindly.
type DiffSession struct {
	ID   string `json:"id" yaml:"id"`
	Base string `json:"base" yaml:"base"`
	// AsOf is the digest of the patch this changeset was computed from - the session's
	// snapshot identity.
	//
	// Without it a client cannot tell a current answer from a frozen one, and the party least
	// able to notice is the one it hurts: an agent cannot see the tree, so it acted with
	// confidence on a changeset that had stopped existing. It also makes "9 of 12 read"
	// meaningful, because the denominator can be shown to be the one those marks were made
	// against.
	AsOf string `json:"as_of,omitempty" yaml:"as_of,omitempty"`
	// Review is the annotated changeset. Recomputed when the working tree moves.
	Diff Diff `json:"diff" yaml:"diff"`
	// Cursor is where the HUMAN is looking.
	Cursor DiffCursor `json:"cursor" yaml:"cursor"`
	// Viewed holds the content digests of hunks the human has marked read. Digests rather
	// than paths-and-line-numbers so the mark survives a rebase that did not touch the hunk -
	// the failing of every viewed-checkbox that resets on force-push.
	Viewed      []string         `json:"viewed,omitempty"      yaml:"viewed,omitempty"`
	Comments    []DiffComment    `json:"comments,omitempty"    yaml:"comments,omitempty"`
	Suggestions []DiffSuggestion `json:"suggestions,omitempty" yaml:"suggestions,omitempty"`
}

// GeneratedCount reports how many files are declared outputs - the ones a reader can fold
// away. It is the headline of the noise-collapse affordance.
func (r Diff) GeneratedCount() int {
	n := 0
	for _, f := range r.Files {
		if f.Generated() {
			n++
		}
	}
	return n
}

// AttachChurn folds the VCS-history lenses onto the review, in place.
//
// It is a separate step from building the review, and the caller supplies the lens data,
// because the two have very different costs and very different freshness needs. The
// annotations are cheap and must be current; the history lenses are a bounded git-log scan
// that the daemon already caches for everyone. Computing them inside Review would either make
// every review pay for a scan or bake a cache into a function that has no business owning one.
// So the daemon passes its cached scan and the CLI passes a fresh one, and this is the single
// definition of how the numbers land on a file either way.
//
// A file with no hotspot entry gets Churn only when its project has a trend, so "quiet file in
// an accelerating project" is still expressible; a file with neither is left nil, because nil
// is what says nobody measured.
func (r Diff) AttachChurn(files []FileHotspot, projects []TrendEntry) {
	rank := make(map[string]int, len(files))
	hot := make(map[string]FileHotspot, len(files))
	for i, f := range files {
		rank[f.Path] = i + 1 // 1-based: "third-hottest" reads, "index 2" does not
		hot[f.Path] = f
	}
	trend := make(map[string]int, len(projects))
	for _, p := range projects {
		trend[p.Path] = p.Delta
	}

	// The lens RAN if it produced any per-file entry at all. Without that check a workspace
	// with no history at all would mark every file NoHistory, which is "nobody looked" wearing
	// the label for "nothing has touched this".
	lensRan := len(files) > 0

	for i := range r.Files {
		f := &r.Files[i]
		h, isHot := hot[f.Path]
		d, hasTrend := trend[f.Project]
		if lensRan && !isHot {
			f.NoHistory = true
		}
		if !isHot && !hasTrend {
			continue
		}
		f.Churn = &DiffChurn{
			Commits:      h.Commits,
			Authors:      h.Authors,
			Score:        h.Score,
			Rank:         rank[f.Path],
			ProjectTrend: d,
		}
	}

	// Re-sort, because this call is what makes NoHistory known: the review was ordered before
	// any history existed, so leaving the old order would keep a ranking key magus now has out
	// of the order it is supposed to drive. SortForReading is idempotent, so re-running it is
	// the cheap way to keep ONE definition of review order rather than a second one here.
	r.SortForReading()
}

// AttachReplay folds the agent trail onto the review, in place.
//
// Supplied by the caller for the same reason AttachChurn's data is: the trail lives beside the
// daemon's cache dir and reading it is a different cost from computing annotations, so who
// pays and how much they read is the caller's decision while the fold stays defined once.
func (r Diff) AttachReplay(byPath map[string][]DiffTouch) {
	if len(byPath) == 0 {
		return
	}
	for i := range r.Files {
		if t, ok := byPath[r.Files[i].Path]; ok {
			r.Files[i].Touches = t
		}
	}
}

// SortForReading orders Files into the sequence magus recommends reading them in. It sorts in
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
//
// A file whose reach was NOT MEASURED sorts above one measured at zero and below any measured
// positive. Unknown is not zero: a measured zero is a promise that nothing references the file,
// and an unmeasured file has made no promise. Ranking the two together is what let an
// unindexed workspace render pure path order while the header still claimed a ranking - see
// Ranked, which is how a caller is supposed to notice.
func (r Diff) SortForReading() {
	rank := func(f DiffFile) int {
		switch f.Role {
		case DiffRoleOutput:
			return 3
		case DiffRoleUnclaimed, DiffRoleMaintained:
			return 2
		default:
			return 1
		}
	}
	// Measured-positive first, then unmeasured, then measured-zero. Encoded as a band so the
	// three cases compare without special-casing nil at every comparison.
	band := func(f DiffFile) int {
		switch {
		case f.Reach == nil:
			return 1
		case *f.Reach > 0:
			return 0
		default:
			return 2
		}
	}
	slices.SortFunc(r.Files, func(a, b DiffFile) int {
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra - rb
		}
		if ba, bb := band(a), band(b); ba != bb {
			return ba - bb
		}
		if a.Reach != nil && b.Reach != nil && *a.Reach != *b.Reach {
			return *b.Reach - *a.Reach // widest reach first
		}
		// A file magus has no history for reads BEFORE one it does, but only as a tiebreak:
		// reach still decides whenever it is known, because "widely referenced" beats "new"
		// as a reason to read something first. This is what stops a new file sinking under
		// files whose only distinction is that they have a past. See DiffFile.NoHistory.
		if a.NoHistory != b.NoHistory {
			if a.NoHistory {
				return -1
			}
			return 1
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

// Ranked reports whether the ordering had a ranking key to work with at all.
//
// False means every file's reach is unmeasured, so SortForReading had nothing to sort on and
// the result is path order wearing a ranking's clothes. Every renderer MUST say so before
// showing the list. The review's Note about an absent symbol index does not cover this: it
// names the missing OVERLAYS (callers, coverage) and never the missing ORDER, and it prints
// after the list - by which point the reader has already formed the belief that the first
// file is the most dangerous one.
func (r Diff) Ranked() bool {
	for _, f := range r.Files {
		if f.Reach != nil {
			return true
		}
	}
	return false
}
