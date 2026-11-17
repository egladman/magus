package job

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
)

// Overlap is two leases whose declared WritePaths intersect. A FACT the
// reader is handed, never a verdict: two leases may share a path because their author
// meant them to run in sequence, or because nobody noticed. Nothing here blocks,
// gates, or reorders anything.
type Overlap struct {
	// JobA and JobB are the lease ids, in ledger order: JobA was recorded first.
	JobA string `json:"job_a" yaml:"job_a"`
	JobB string `json:"job_b" yaml:"job_b"`
	// PathsA and PathsB are the intersecting declarations from each side, deduped and
	// kept apart. They are rarely the same string ("internal/ledger" and
	// "internal/ledger/store.go" intersect), so one merged list left a reader unable to
	// tell which lease claimed which, which is the only thing they can act on.
	PathsA []string `json:"paths_a" yaml:"paths_a"`
	PathsB []string `json:"paths_b" yaml:"paths_b"`
	// Claims compares what the two sides DECLARED below the file, when either claims a
	// declaration (`run.go#executeStages`): ClaimsDisjoint for different declarations of the
	// files they share, ClaimsShared when a claim covers one the other side holds. Empty
	// when neither side claims below the file.
	Claims string `json:"claims,omitempty" yaml:"claims,omitempty"`
	// Footprint compares what the two jobs have actually changed, declaration by
	// declaration. Nil when no reader computed it: it needs each job's checkout and a VCS,
	// and deriving an overlap needs neither.
	Footprint *OverlapFootprint `json:"footprint,omitzero" yaml:"footprint,omitempty"`
}

// The verdicts a Overlap's Claims reaches.
const (
	ClaimsDisjoint = "disjoint"
	ClaimsShared   = "shared"
)

// The verdicts a OverlapFootprint reaches.
const (
	FootprintDisjoint = "disjoint"
	FootprintShared   = "shared"
	FootprintUnknown  = "unknown"
)

// OverlapFootprint is whether two overlapping jobs' diffs touch the same declaration.
// Like the overlap it hangs off, a report: two jobs sharing a function may have been
// planned that way.
type OverlapFootprint struct {
	// Verdict is FootprintDisjoint, FootprintShared or FootprintUnknown.
	Verdict string `json:"verdict" yaml:"verdict"`
	// Shared is the Collisions of the two footprints, each Location as its String, on either
	// side of either diff, sorted. A changed line above a file's first declaration, or in a
	// file with no diff driver, locates as the whole file and so collides with every region
	// of that file.
	Shared []string `json:"shared,omitempty" yaml:"shared,omitempty"`
	// Reason says why the verdict is FootprintUnknown.
	Reason string `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// List is what a reader of the ledger is served: the recorded jobs, plus
// the overlaps derived from them. It has a constructor rather than a literal
// at each read door. The MCP tool and the console's route must not disagree
// about whether an overlap exists, as with types.NewFileReport.
type List struct {
	Jobs []Job `json:"jobs" yaml:"jobs"`
	// Attempts are the tries of Jobs the store holds, each read under its
	// job's deadline (see [Attempt.Bound]).
	Attempts []Attempt `json:"attempts,omitempty" yaml:"attempts,omitempty"`
	Overlaps []Overlap `json:"overlaps,omitempty" yaml:"overlaps,omitempty"`
	// Overdue, Orphans and Stale are live job ids a reader should look at,
	// derived at one instant by [List.Flag]. The store's read has ended
	// most orphans and untaken stale jobs (see job.Store.List).
	Overdue []string `json:"overdue,omitempty" yaml:"overdue,omitempty"`
	Orphans []string `json:"orphans,omitempty" yaml:"orphans,omitempty"`
	Stale   []string `json:"stale,omitempty"   yaml:"stale,omitempty"`
	// Blocked are the live jobs that claim no paths yet because a dependency
	// has not passed, each naming the first such dependency. Derived with
	// Overlaps.
	Blocked []Block `json:"blocked,omitempty" yaml:"blocked,omitempty"`
	// ReadOnly are the jobs requiring a feature this build lacks. It lists and
	// honors them and refuses every write to them.
	ReadOnly []ReadOnly `json:"read_only,omitempty" yaml:"read_only,omitempty"`
	// Unmerged is filled only by a listing that asks for it. It reads the
	// version control per job, and every other door lists on a hot path.
	Unmerged *UnmergedReport `json:"unmerged,omitzero" yaml:"unmerged,omitempty"`
}

// ReadOnly names a job this build will not write and the features it lacks.
type ReadOnly struct {
	Job   string   `json:"job"   yaml:"job"`
	Lacks []string `json:"lacks" yaml:"lacks"`
}

// Block is why a live job owns none of its write paths: On, a job it depends on, is in
// State rather than pass. State is empty when no job declares On.
type Block struct {
	Job   string `json:"job"             yaml:"job"`
	On    string `json:"on"              yaml:"on"`
	State State  `json:"state,omitempty" yaml:"state,omitempty"`
}

// BlockedOn reports the first job row depends on that has not reached pass.
// A blocked job claims none of its write paths, so the overlap report and the
// guard ask this before treating a job as an owner.
//
// A dependency no job declares blocks too.
func BlockedOn(rows []Job, row Job) (Block, bool) {
	for _, dep := range row.DependsOn {
		i := slices.IndexFunc(rows, func(r Job) bool { return r.ID == dep })
		if i < 0 {
			return Block{Job: row.ID, On: dep}, true
		}
		if rows[i].State != StatePass {
			return Block{Job: row.ID, On: dep, State: rows[i].State}, true
		}
	}
	return Block{}, false
}

// String renders the reason as "blocked on <dep> which is <state>".
func (b Block) String() string {
	state := string(b.State)
	if state == "" {
		state = "undeclared"
	}
	return fmt.Sprintf("blocked on %s which is %s", b.On, state)
}

// Flag fills Overdue, Orphans and Stale as of now, in unix seconds. Overdue is past its
// deadline; an orphan is live while an ancestor has ended (pass, fail or no_return), so
// nobody is left to wait on it; stale was not updated within staleAfter, read by
// [StaleAt], and a zero staleAfter flags nothing.
func (l List) Flag(now int64, staleAfter time.Duration) List {
	l.Overdue, l.Orphans, l.Stale = nil, nil, nil
	for _, row := range l.Jobs {
		if !row.State.Live() {
			continue
		}
		if row.Overdue(now) {
			l.Overdue = append(l.Overdue, row.ID)
		}
		if _, ended := EndedAncestor(l.Jobs, row.ID); ended {
			l.Orphans = append(l.Orphans, row.ID)
		}
		if StaleAt(l.Jobs, row, now, staleAfter) {
			l.Stale = append(l.Stale, row.ID)
		}
	}
	return l
}

// EndedAncestor returns the nearest ancestor of id in a terminal state. A job
// with no state has not said it ended, so it does not count.
func EndedAncestor(rows []Job, id string) (Job, bool) {
	for _, a := range Ancestors(rows, id) {
		if a.State.Terminal() {
			return a, true
		}
	}
	return Job{}, false
}

// Ancestors walks id's parent chain nearest first. It stops at a parent the
// jobs do not carry and at a cycle, since either means the plan is damaged.
//
// A scan per step rather than an index: chains are a few jobs deep, and every
// list flags each live job through this. A map of the whole store per call
// made one list quadratic in allocations. The chain walked so far is the
// cycle check.
func Ancestors(rows []Job, id string) []Job {
	find := func(id string) (Job, bool) {
		i := slices.IndexFunc(rows, func(r Job) bool { return r.ID == id })
		if i < 0 {
			return Job{}, false
		}
		return rows[i], true
	}
	var out []Job
	seen := func(id string) bool { return slices.ContainsFunc(out, func(a Job) bool { return a.ID == id }) }
	cur, ok := find(id)
	for ok && cur.Parent != "" && cur.Parent != id && !seen(cur.Parent) {
		if cur, ok = find(cur.Parent); ok {
			out = append(out, cur)
		}
	}
	return out
}

// Descendants are the jobs below id by parent chain, in store order.
func Descendants(rows []Job, id string) []Job {
	var out []Job
	for _, r := range rows {
		if r.ID != id && slices.ContainsFunc(Ancestors(rows, r.ID), func(a Job) bool { return a.ID == id }) {
			out = append(out, r)
		}
	}
	return out
}

// NewList wraps the jobs and derives the overlaps, on READ and never stored.
//
// ReportedBase, BaseVerdict and Registered are NOT derived here: they live on
// the job and are computed once, when the worker registers.
//
// This is the single door onto a report, so the empty case is normalized HERE.
// An unwritten ledger serves "jobs":[] rather than null.
func NewList(jobs []Job) List {
	if jobs == nil {
		jobs = []Job{}
	}
	var blocked []Block
	var readOnly []ReadOnly
	for _, row := range jobs {
		if b, ok := BlockedOn(jobs, row); ok && row.State.Live() {
			blocked = append(blocked, b)
		}
		if lacks := row.Unmet(); lacks != nil {
			readOnly = append(readOnly, ReadOnly{Job: row.ID, Lacks: lacks})
		}
	}
	return List{Jobs: jobs, Overlaps: jobOverlaps(jobs), Blocked: blocked, ReadOnly: readOnly}
}

// jobOverlaps reports every pair of jobs whose declared write paths
// intersect, in ledger order.
//
// Only a [State.Editing] job is in a pair. A released, exited or finished job is not
// competing for a path (that is the whole shape of the skill's early-release rule,
// where a worker shrinks its write paths so a waiter can start), and reporting one
// would make the report noisiest exactly when the plan is winding down. A job blocked on
// a dependency is not in any pair either: it claims nothing until that dependency passes.
func jobOverlaps(jobs []Job) []Overlap {
	claims := func(j Job) bool {
		_, blocked := BlockedOn(jobs, j)
		return j.State.Editing() && len(j.HardPaths()) > 0 && !blocked
	}
	var out []Overlap
	for i, a := range jobs {
		if !claims(a) {
			continue
		}
		for _, b := range jobs[i+1:] {
			if !claims(b) {
				continue
			}
			if pa, pb := intersectingPaths(a.HardPaths(), b.HardPaths()); len(pa) > 0 {
				out = append(out, Overlap{JobA: a.ID, JobB: b.ID, PathsA: pa, PathsB: pb, Claims: claimsVerdict(pa, pb)})
			}
		}
	}
	return out
}

// claimsVerdict is ClaimsDisjoint when the only ground two sides share is a file each
// claims different declarations of, ClaimsShared when a declaration claim meets a claim
// that covers it, and "" when neither side claims below the file.
func claimsVerdict(pathsA, pathsB []string) string {
	claimed := func(p string) bool { _, d := SplitClaim(p); return d != "" }
	if !slices.ContainsFunc(pathsA, claimed) && !slices.ContainsFunc(pathsB, claimed) {
		return ""
	}
	for _, pa := range pathsA {
		for _, pb := range pathsB {
			if PathsIntersect(pa, pb) {
				return ClaimsShared
			}
		}
	}
	return ClaimsDisjoint
}

// intersectingPaths collects the declared paths that cover common ground at the file
// level, each side kept in its own list and deduped. Two claims on different declarations
// of one file are collected: they are an integration order, and the pair says so through
// its Claims verdict rather than by disappearing.
func intersectingPaths(a, b []string) (pathsA, pathsB []string) {
	for _, pa := range a {
		for _, pb := range b {
			if !pathsShareGround(pa, pb) {
				continue
			}
			if !slices.Contains(pathsA, pa) {
				pathsA = append(pathsA, pa)
			}
			if !slices.Contains(pathsB, pb) {
				pathsB = append(pathsB, pb)
			}
		}
	}
	return pathsA, pathsB
}

// PathsIntersect decides whether two DECLARED paths cover common ground: containment on
// the cleaned paths, with a glob truncated to the literal prefix it can be judged by.
//
// THE definition, rather than a copy of one. The overlap report below asks it of two
// leases; the worker brief asks it of a lease's write paths against a project's declared
// output globs, which is the same question about a different pair of declarations. A
// second implementation would differ on the day the truncation rule changed.
//
// Truncating is deliberate over-reporting. "console/src/**/*.ts" and
// "console/src/**/*.css" cover no common file, and this reports them anyway because
// both reduce to "console/src"; deciding whether two arbitrary globs can ever match
// one path is a solver, and a missed collision costs a reader far more than a pair
// they look at and dismiss.
//
// Two claims on one file naming different declarations (`run.go#A`, `run.go#B`) do not
// intersect; a claim with a declaration intersects any entry covering its file without one.
// Declarations are compared as written, so `run.go#A` and `run.go#func A() {` read as
// disjoint although both name one function: the footprint is what catches that pair.
func PathsIntersect(a, b string) bool {
	if !pathsShareGround(a, b) {
		return false
	}
	_, declA := SplitClaim(a)
	_, declB := SplitClaim(b)
	return declA == "" || declB == "" || declA == declB
}

// pathsShareGround is PathsIntersect at the file level, declarations ignored.
func pathsShareGround(a, b string) bool {
	// An entry that names nothing claims nothing. It cleans to ".". The
	// whole-tree rule below would read that as a claim on everything.
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	if lit, glob, ok := literalAgainstGlob(a, b); ok {
		return globReaches(glob, lit)
	}
	a, b = LiteralPrefix(a), LiteralPrefix(b)
	if a == "" || b == "" {
		// A pattern with no literal prefix ("**/*.go") claims the whole tree.
		return true
	}
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// literalAgainstGlob returns the cleaned literal and the cleaned glob, and true, when
// exactly one of a and b holds a glob metacharacter and the literal is not the root. Any
// other pair reports false: two literals and two globs keep the prefix rule, since deciding
// whether two globs share a path is a solver and a missed collision costs more than a pair
// read and dismissed.
func literalAgainstGlob(a, b string) (lit, glob string, ok bool) {
	a, _ = SplitClaim(a)
	b, _ = SplitClaim(b)
	a, b = path.Clean(a), path.Clean(b)
	switch aMeta, bMeta := isGlobMeta(a), isGlobMeta(b); {
	case aMeta && !bMeta && b != ".":
		return b, a, true
	case bMeta && !aMeta && a != ".":
		return a, b, true
	}
	return "", "", false
}

// globReaches reports whether glob can match lit, an ancestor of lit, or a path beneath
// lit, segment by segment, so "internal/guard/*_test.go" reaches "internal/guard" and not
// "internal/guard/guard.go". A `**` segment can absorb anything below it and so answers
// true, as does a glob that cannot be split on "/" (see [unsplittableGlob]): the answer
// errs toward overlapping.
func globReaches(glob, lit string) bool {
	if unsplittableGlob(glob) {
		return true
	}
	litSegs := strings.Split(lit, "/")
	for i, seg := range strings.Split(glob, "/") {
		if strings.Contains(seg, "**") || i >= len(litSegs) {
			return true
		}
		if ok, err := doublestar.Match(seg, litSegs[i]); err == nil && !ok {
			return false
		}
	}
	return true
}

// unsplittableGlob reports whether glob cannot be read one "/" segment at a time: a "/"
// sits inside a {...} alternation, or the braces do not balance, so no split is sure to
// keep an alternation whole.
func unsplittableGlob(glob string) bool {
	depth, slash := 0, false
	for _, r := range glob {
		switch {
		case r == '{':
			depth++
		case r == '}':
			depth--
		case r == '/' && depth > 0:
			slash = true
		}
	}
	return slash || depth != 0
}

// LiteralPrefix is the part of a declared path that names actual directories: the
// cleaned path itself when it holds no glob metacharacter, and everything above the
// first segment that does when it holds one.
//
// Exported because the focus rule asks the same question of the same
// declarations. A wildcard cannot say which project a write path lands in.
// Two packages deriving that prefix by their own rules would disagree the
// day the rules drifted. A claimed declaration (see SplitClaim) is
// not part of the path and is dropped.
func LiteralPrefix(p string) string {
	p, _ = SplitClaim(p)
	p = path.Clean(p)
	if p == "." || p == "/" {
		return ""
	}
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if strings.ContainsAny(s, "*?[{") {
			return strings.Join(segs[:i], "/")
		}
	}
	return p
}

// PathWithin reports whether the declared path inner names no file outer does not, so a
// holder replacing outer with inner only narrows. A literal is within a glob that matches
// it, a literal names itself and everything beneath it, `F#d` is within `F` and within a
// glob matching F, and a glob is within a literal directory holding its literal prefix or
// within `<prefix>/**` and `<prefix>/**/<last>` when its prefix is under that one. Against
// `<prefix>/**/*<suffix>`, a last segment glob is within it when every name it matches
// ends in the literal suffix: `internal/job/*_test.go` is within `internal/**/*.go`.
//
// It errs toward false: two globs of any other shape are never within one another, since
// proving one glob's matches a subset of another's is a solver, and a refused narrowing
// costs the holder one widening where a wrong yes hands it a file nobody granted.
func PathWithin(inner, outer string) bool {
	innerFile, innerDecl := SplitClaim(inner)
	outerFile, outerDecl := SplitClaim(outer)
	if innerFile == "" || outerFile == "" {
		return false
	}
	innerFile, outerFile = path.Clean(innerFile), path.Clean(outerFile)
	innerGlob := isGlobMeta(innerFile)
	switch {
	case outerDecl != "":
		return innerDecl == outerDecl && innerFile == outerFile && !innerGlob
	case innerFile == outerFile, outerFile == ".":
		return true
	case !isGlobMeta(outerFile):
		return underDir(LiteralPrefix(innerFile), outerFile)
	case !innerGlob:
		ok, err := doublestar.Match(outerFile, innerFile)
		return err == nil && ok
	}
	prefix, last, ok := starStarShape(outerFile)
	if !ok || !underDir(LiteralPrefix(path.Dir(innerFile)), prefix) {
		return false
	}
	base := path.Base(innerFile)
	if last == "" || base == last {
		return true
	}
	if isGlobMeta(base) {
		return segmentWithin(base, last)
	}
	matched, err := doublestar.Match(last, base)
	return err == nil && matched
}

// segmentWithin reports whether every name the glob segment inner matches is one the
// segment outer matches, for an outer of the form `*<suffix>` with a literal suffix. A
// match for inner always ends in the literal run after inner's last metacharacter, so
// that run ending in the suffix is enough, and an inner ending in a metacharacter, or
// `**`, can end in anything.
func segmentWithin(inner, outer string) bool {
	if inner == "**" || !strings.HasPrefix(outer, "*") || isGlobMeta(outer[1:]) {
		return false
	}
	suffix := outer[1:]
	if suffix == "" {
		return true
	}
	tail := inner[strings.LastIndexAny(inner, "*?[]{}")+1:]
	return tail != "" && strings.HasSuffix(tail, suffix)
}

// underDir reports whether p is dir or beneath it; every path is under the root, "".
func underDir(p, dir string) bool {
	return dir == "" || p == dir || strings.HasPrefix(p, dir+"/")
}

// starStarShape splits a glob of the form `<prefix>/**` or `<prefix>/**/<last>`, with a
// literal prefix and a last segment holding no `**`, and reports false for any other shape.
func starStarShape(glob string) (prefix, last string, ok bool) {
	if unsplittableGlob(glob) {
		return "", "", false
	}
	prefix = LiteralPrefix(glob)
	rest := strings.TrimPrefix(strings.TrimPrefix(glob, prefix), "/")
	segs := strings.Split(rest, "/")
	switch {
	case segs[0] != "**":
		return "", "", false
	case len(segs) == 1:
		return prefix, "", true
	case len(segs) == 2 && !strings.Contains(segs[1], "**"):
		return prefix, segs[1], true
	}
	return "", "", false
}

// isGlobMeta reports whether s holds a glob metacharacter.
func isGlobMeta(s string) bool { return strings.ContainsAny(s, "*?[{") }
