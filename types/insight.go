package types

import (
	"context"
	"time"
)

// InsightDefinition is the umbrella description of the insight lenses.
const InsightDefinition = "Insight shows where a codebase's attention and risk concentrate. " +
	"Four lenses read VCS history: hotspots (churn x complexity, the prime refactoring targets), " +
	"affinity (projects that change together, and whether a dependency edge explains it), " +
	"ownership (author concentration and bus factor), and trend (rising vs cooling activity). " +
	"A fifth lens, volatility, reads run-outcome history instead: targets whose pass/fail record " +
	"flaps (a Wilson-scored flakiness signal). A sixth, unreferenced, reads the knowledge graph: " +
	"code symbols nothing else in the workspace names."

// InsightOptions configures an insight scan. One scan of recent history feeds every
// lens; Dir scopes it to a subtree, Since bounds it by date, Files switches the
// hotspots lens from project to file granularity.
type InsightOptions struct {
	Dir     string
	Commits int
	Since   string
	Files   bool
}

// Per-lens descriptions, shown by each lens and reused in the combined report.
const (
	HotspotDefinition = "Hotspots are files (or projects) where edit frequency meets " +
		"complexity - the prime refactoring targets: code both churned often and hard to understand."
	AffinityDefinition = "Affinity is how often projects change in the same commit (temporal " +
		"coupling). A hidden pair has affinity without either declaring a dependency on the other " +
		"- a candidate architectural smell."
	OwnershipDefinition = "Ownership shows author concentration: who touches each project most, " +
		"how many distinct authors it has (bus factor), and whether it has gone quiet (abandonment risk)."
	TrendDefinition = "Trend compares the recent and earlier halves of the window: a positive " +
		"delta is a rising hotspot (accelerating activity), a negative one is cooling."
	VolatilityDefinition = "Volatility reads run-outcome history, not git: each (project, target) " +
		"pair's recent pass/fail/volatile record scored by its Wilson lower bound. A pair at or above " +
		"the configured threshold is flagged volatile - a flakiness signal, the prime stabilization targets."
	UnreferencedDefinition = "Unreferenced lists code symbols the workspace defines and nothing " +
		"in it names: no call from another symbol, and no file outside the one defining them. It reads " +
		"the SCIP-backed knowledge graph, not git. This is a measurement, not a verdict - reflection, " +
		"interface dispatch, build tags, generated call sites, and any consumer outside this workspace " +
		"are all invisible to it, so read each entry before deleting anything."
)

// DuplicationDefinition is the lens's contract, stated where a reader of the output meets it.
const DuplicationDefinition = "Duplication ranks pairs of functions that call the same workspace " +
	"symbols in the same proportions, weighting each shared callee by how rare it is, which is what " +
	"copied logic looks like from the call graph. It reads the SCIP-backed knowledge graph, so it " +
	"compares every indexed language the same way and parses no source. It sees only calls to symbols " +
	"this workspace DEFINES: logic duplicated entirely in standard-library or dependency calls leaves " +
	"both functions with empty call sets and is invisible here. A measurement, not a verdict - two " +
	"functions can share a shape on purpose, so read both before folding them."

// DuplicationOptions are the thresholds a pair must clear to be reported. Every one is a
// noise filter, and each has a failure mode in both directions: too low and the list is
// sibling functions nobody would fold, too high and a real copy goes unmentioned. They
// are configuration rather than constants because where that line sits depends on the
// codebase, and a repository of small single-purpose helpers pairs far more readily than
// one of large functions.
type DuplicationOptions struct {
	// MinCallees is how many distinct workspace symbols a function must call to be
	// compared at all.
	MinCallees int
	// MinShared is how many callees a pair must have in common. The absolute floor the
	// score cannot provide: three identical calls score a perfect 1.0, and three shared
	// helpers is two callers of a small toolkit far more often than it is a copy.
	MinShared int
	// MinScore is the rarity-weighted overlap, from 0 to 1, a pair must reach.
	MinScore float64
	// MinSpanRatio is the shorter body's length over the longer's. Below it the two are
	// too different in size to be the same logic.
	MinSpanRatio float64
	// IncludeTests compares test functions too. Off by default: sibling tests call the same
	// setup helpers by design, and they bury every finding a person would act on.
	IncludeTests bool
}

// DuplicationOutput is the ranked pairs, with the coverage verdict that says whether a
// short list means little duplication or a half-indexed workspace.
type DuplicationOutput struct {
	Definition string             `json:"definition" yaml:"definition"`
	Groups     []DuplicationGroup `json:"groups"     yaml:"groups"`
	Answer     KnowledgeAnswer    `json:"answer"     yaml:"answer"`
}

// DuplicationGroup is functions that are copies of each other, and the evidence: the
// weakest pairwise score inside the group, and the workspace symbols EVERY member calls.
//
// A group rather than a pair, because a family is one decision. Five RPC methods sharing
// one dial-send-decode shape were ten pairs, and a reader acting on the list folds all five
// or none. Shared is what a fold would extract, so it is the intersection across members,
// and Score is the minimum so a loosely chained group cannot read as tighter than its
// weakest link.
type DuplicationGroup struct {
	Members []DuplicationSite `json:"members" yaml:"members"`
	Score   float64           `json:"score"   yaml:"score"`
	Shared  []string          `json:"shared"  yaml:"shared"`
	// Packages are the distinct packages the members live in, as the SCIP namespace symbol
	// IDs the index defines them by, so each one is a `magus refs` argument.
	Packages []string `json:"packages" yaml:"packages"`
	// Placement is where a fold can put the shared helper without creating an import
	// cycle, and Home the package it names (empty unless member or dependency).
	//
	// A group whose copies cannot be folded without a cycle is not the finding it looks
	// like, and one whose only home is a new package is a bigger change than one whose
	// home already exists, so the reader needs this before deciding.
	Placement DuplicationPlacement `json:"placement" yaml:"placement"`
	Home      string               `json:"home,omitempty" yaml:"home,omitempty"`
	// Shape is the fold the evidence points at, and Removable the lines it would save. A
	// score says two bodies look alike; neither says whether folding them is worth the
	// abstraction it adds, which is the decision a reader is actually making.
	Shape     DuplicationShape `json:"shape"     yaml:"shape"`
	Removable int              `json:"removable" yaml:"removable"`
	// Siblings reports that every member has the same name: implementations of one method
	// on different types, which fold into a helper each calls rather than into one function.
	Siblings bool `json:"siblings" yaml:"siblings"`
	// History is how the members' files changed together over recent commits, or nil when
	// the members share one file or no history could be read.
	History *DuplicationHistory `json:"history,omitempty" yaml:"history,omitempty"`
}

// DuplicationShape names the fold a duplication group's evidence supports.
type DuplicationShape string

const (
	// ShapeParameterize means the members call nothing the others do not: they differ only
	// in values, so one function taking those values as parameters replaces them.
	ShapeParameterize DuplicationShape = "parameterize"
	// ShapeSharedCore means each member also calls something of its own. The shared calls
	// move into a helper and each member keeps what is its own, rather than folding the
	// members into one function steered by a callback or a flag.
	ShapeSharedCore DuplicationShape = "shared-core"
	// ShapeSiblingHelper means the members are same-named implementations on different
	// types. They stay, and call one helper holding what they share.
	ShapeSiblingHelper DuplicationShape = "sibling-helper"
	// ShapeLeave means a fold would not shorten anything: the helper and the call sites it
	// needs cost about what the copies do.
	ShapeLeave DuplicationShape = "leave"
)

// DuplicationHistory is co-change evidence for a group spanning several files, over the
// commits scanned. Copies that always change together are drifting the moment one is
// edited alone, which is the case for folding; copies edited apart may be diverging on
// purpose, which is the case against.
type DuplicationHistory struct {
	// Commits is the window scanned.
	Commits int `json:"commits" yaml:"commits"`
	// Together counts commits touching every member's file.
	Together int `json:"together" yaml:"together"`
	// Apart counts commits touching some members' files but not all.
	Apart int `json:"apart" yaml:"apart"`
}

// DuplicationPlacement is where a duplication group's shared helper can live without an
// import cycle, judged from the imports the SCIP index records: a file that references a
// namespace symbol imports it, and the namespace the file defines is the importer.
type DuplicationPlacement string

const (
	// PlacementMember is one of the members' own packages, which every other member can
	// import without a cycle. The cheapest fold: no new package.
	PlacementMember DuplicationPlacement = "member"
	// PlacementDependency is a package every member already depends on, and which can
	// import what the helper calls. No new import edge in any member.
	PlacementDependency DuplicationPlacement = "dependency"
	// PlacementNewPackage means no existing package works, and a new package that imports
	// the shared callees and is imported by every member would not cycle.
	PlacementNewPackage DuplicationPlacement = "new-package"
	// PlacementBlocked means a shared callee's package depends on a member's, so any home
	// that can call the callees is one that member cannot import. Folding it needs code
	// moved first.
	PlacementBlocked DuplicationPlacement = "blocked"
	// PlacementUnknown means a member's index names no namespace for it, so its imports
	// cannot be read and no home can be vouched for.
	PlacementUnknown DuplicationPlacement = "unknown"
)

// DuplicationSite is one member of a group, with the span that bounds its body so a reader
// can open them all at once, and what sets it apart from the others.
type DuplicationSite struct {
	ID      string `json:"id"               yaml:"id"`
	Label   string `json:"label"            yaml:"label"`
	Source  string `json:"source,omitempty" yaml:"source,omitempty"`
	EndLine int    `json:"end_line"         yaml:"end_line"`
	// Distinct are the workspace symbols this member calls that not every member does: the
	// part a fold would have to parameterize or leave behind.
	Distinct []string `json:"distinct" yaml:"distinct"`
	// Callers counts the functions that call this member.
	Callers int `json:"callers" yaml:"callers"`
	// ForeignFiles counts files outside this member's package that name it. A member named
	// from elsewhere is an entry point, whose signature a fold has to keep.
	ForeignFiles int `json:"foreign_files" yaml:"foreign_files"`
	// Tested reports that a test file names this member directly. A fold of untested code
	// has nothing to say it preserved behavior.
	Tested bool `json:"tested" yaml:"tested"`
}

// UnreferencedOutput lists the symbols nothing in the workspace names.
//
// Answer is what keeps the list honest. A project whose symbol index was never built
// contributes no symbols at all, so its dead code would silently render as a clean
// report; the verdict says when the list is a fact and when it is only what magus could
// see. An empty Symbols list with an unknown verdict means "nothing found and I could not
// look everywhere", which is not the same as "nothing to find".
type UnreferencedOutput struct {
	Definition string              `json:"definition" yaml:"definition"`
	Symbols    []UnreferencedEntry `json:"symbols"    yaml:"symbols"`
	Answer     KnowledgeAnswer     `json:"answer"     yaml:"answer"`
}

// UnreferencedEntry is one symbol nothing names, with where it is defined so the reader
// can go look at it. Kind is the SCIP classifier (Function, Struct, ...), which is what
// makes the list triageable: an unreferenced exported Function reads very differently
// from an unreferenced Field.
type UnreferencedEntry struct {
	ID       string `json:"id"                 yaml:"id"`
	Label    string `json:"label"              yaml:"label"`
	Source   string `json:"source,omitempty"   yaml:"source,omitempty"`
	Kind     string `json:"kind,omitempty"     yaml:"kind,omitempty"`
	Language string `json:"language,omitempty" yaml:"language,omitempty"`
}

// HotspotOutput ranks where churn meets complexity — the canonical "fix this first"
// view. Nodes is the project-level heatmap (reusing the dependency-graph nodes, with
// churn/authors/recency/blast-radius/CI-duration); Files is the per-file ranking.
type HotspotOutput struct {
	Definition string        `json:"definition" yaml:"definition"`
	Commits    int           `json:"commits"    yaml:"commits"`
	Since      string        `json:"since,omitempty" yaml:"since,omitempty"`
	Nodes      []Node        `json:"nodes"      yaml:"nodes"`
	Files      []FileHotspot `json:"files,omitempty" yaml:"files,omitempty"`
}

// FileHotspot is one file's hotspot score: edit frequency weighted by complexity.
type FileHotspot struct {
	Path       string    `json:"path"                  yaml:"path"`
	Commits    int       `json:"commits"               yaml:"commits"`
	Complexity int       `json:"complexity"            yaml:"complexity"`
	Score      int       `json:"score"                 yaml:"score"` // commits × complexity
	Authors    int       `json:"authors"               yaml:"authors"`
	LastCommit time.Time `json:"last_commit,omitempty" yaml:"last_commit,omitempty"`
	// Moves is how many times the file changed path inside the window: the count of
	// distinct names its history folded in, minus the one it ends under. A file that
	// keeps changing address is churning architecturally rather than just textually,
	// which is a different thing to know than its edit count and is not derivable
	// from Path alone.
	Moves int `json:"moves,omitempty" yaml:"moves,omitempty"`
}

// AffinityOutput reports projects that change together (temporal coupling). Hidden
// pairs are the interesting ones: they co-change but no dependency edge connects them.
type AffinityOutput struct {
	Definition string     `json:"definition" yaml:"definition"`
	Commits    int        `json:"commits"    yaml:"commits"`
	Since      string     `json:"since,omitempty" yaml:"since,omitempty"`
	Pairs      []CoChange `json:"pairs"      yaml:"pairs"`
}

// CoChange is a pair of projects that changed together, how often, and whether the
// affinity is "hidden" — i.e. neither project declares a dependency on the other.
type CoChange struct {
	A      string `json:"a"      yaml:"a"`
	AName  string `json:"a_name" yaml:"a_name"`
	B      string `json:"b"      yaml:"b"`
	BName  string `json:"b_name" yaml:"b_name"`
	Count  int    `json:"count"  yaml:"count"`
	Hidden bool   `json:"hidden,omitempty" yaml:"hidden,omitempty"`
}

func (c CoChange) ALabel() string { return ProjectDisplayName(c.A, c.AName, "") }
func (c CoChange) BLabel() string { return ProjectDisplayName(c.B, c.BName, "") }

// OwnershipOutput reports author concentration per project — the knowledge-risk view.
type OwnershipOutput struct {
	Definition string           `json:"definition" yaml:"definition"`
	Commits    int              `json:"commits"    yaml:"commits"`
	Since      string           `json:"since,omitempty" yaml:"since,omitempty"`
	Projects   []OwnershipEntry `json:"projects"   yaml:"projects"`
}

// OwnershipEntry is one project's authorship: how many distinct authors touched it, who
// touched it most (and their share), whether it is bus-factor-1 (a single author),
// and whether it has gone quiet in the recent half of the window (abandonment risk).
type OwnershipEntry struct {
	Path         string    `json:"path"                   yaml:"path"`
	Name         string    `json:"name"                   yaml:"name"`
	Commits      int       `json:"commits"                yaml:"commits"`
	Authors      int       `json:"authors"                yaml:"authors"`
	Primary      string    `json:"primary"                yaml:"primary"`
	PrimaryShare int       `json:"primary_share"          yaml:"primary_share"` // percent
	BusFactor1   bool      `json:"bus_factor_1,omitempty" yaml:"bus_factor_1,omitempty"`
	Stale        bool      `json:"stale,omitempty"        yaml:"stale,omitempty"`
	LastCommit   time.Time `json:"last_commit,omitempty"  yaml:"last_commit,omitempty"`
}

func (o OwnershipEntry) Label() string { return ProjectDisplayName(o.Path, o.Name, "") }

// TrendOutput ranks projects by whether their activity is rising or cooling — the
// window is split at its midpoint and the two halves compared.
type TrendOutput struct {
	Definition string       `json:"definition" yaml:"definition"`
	Commits    int          `json:"commits"    yaml:"commits"`
	Since      string       `json:"since,omitempty" yaml:"since,omitempty"`
	Projects   []TrendEntry `json:"projects"   yaml:"projects"`
}

// TrendEntry is one project's churn split across the window's two halves; Delta>0 is rising.
type TrendEntry struct {
	Path    string `json:"path"    yaml:"path"`
	Name    string `json:"name"    yaml:"name"`
	Recent  int    `json:"recent"  yaml:"recent"`
	Earlier int    `json:"earlier" yaml:"earlier"`
	Delta   int    `json:"delta"   yaml:"delta"`
}

func (t TrendEntry) Label() string { return ProjectDisplayName(t.Path, t.Name, "") }

// InsightView bundles the four VCS-history lenses plus the run-outcome volatility lens,
// without the knowledge-graph axis. It is what the console serves at GET /api/v1/insight:
// the same per-lens outputs the CLI produces. The four git lenses come from one bounded
// git-log scan (cached by the service); Volatility is a fresh runtime-history file read
// folded into the same response, so the dashboard reads one endpoint for every lens.
type InsightView struct {
	Hotspots  HotspotOutput   `json:"hotspots"   yaml:"hotspots"`
	Affinity  AffinityOutput  `json:"affinity"   yaml:"affinity"`
	Ownership OwnershipOutput `json:"ownership"  yaml:"ownership"`
	Trend     TrendOutput     `json:"trend"      yaml:"trend"`
	// Volatility stays a POINTER here while InsightReport's is a value, and the two
	// are not carelessly out of step: this is the console's wire shape, where absent
	// and empty say different things. docs/reference/api/insight.md commits to it
	// (null renders "no runs recorded yet", an empty report renders "no volatile
	// targets"), and volatilityToProto maps nil to a nil message to preserve it.
	// InsightReport has no such reader: it crosses into Buzz, where the mirror
	// declares the field non-optional so a magusfile reaches .volatility.targets
	// without a nil guard.
	Volatility *VolatilityReport `json:"volatility" yaml:"volatility"`
}

// InsightReport bundles every lens for the combined report (the committable
// Markdown doc and its -o json form). The VCS axis only: `magus graph stats` is
// the structural one, and nothing here reads the knowledge graph.
//
// Volatility is a VALUE, not a pointer, so the Buzz mirror declares it non-optional and
// a caller reads report.volatility.targets without a nil guard. An empty Targets list is
// what "the run-outcome axis had nothing to say" means, which is the same test every
// consumer already made. Deliberately unlike InsightView's pointer above; see the note
// there for why the console shape keeps a distinction this one does not need.
type InsightReport struct {
	Hotspots   HotspotOutput    `json:"hotspots"    yaml:"hotspots"`
	Affinity   AffinityOutput   `json:"affinity"    yaml:"affinity"`
	Ownership  OwnershipOutput  `json:"ownership"   yaml:"ownership"`
	Trend      TrendOutput      `json:"trend"       yaml:"trend"`
	Volatility VolatilityReport `json:"volatility"  yaml:"volatility"`
	// Unreferenced is the knowledge-graph axis. Like Volatility it is a value, not a
	// pointer: the report always renders the section, and an empty list with a verdict
	// says more than an omitted section would.
	Unreferenced UnreferencedOutput `json:"unreferenced" yaml:"unreferenced"`
	// Duplication is the other knowledge-graph axis, a value for the same reason.
	Duplication DuplicationOutput `json:"duplication" yaml:"duplication"`
}

// InsightAnalyzer is the optional capability a workspace implements to answer the
// insight lenses. It is the sibling of [IgnoredFileReporter] and [ConflictResolver]:
// callers type-assert for it and degrade when it is absent rather than requiring
// every WorkspaceRepository to carry analytics it may have no history to compute.
//
// It exists so both entry points agree on ONE vocabulary. The CLI declared this
// shape privately and the Buzz surface could not see it, so `magus\insight`
// forked a whole magus (a process spawn, a second workspace load, a JSON encode and
// a Buzz-side parse) to reach methods the calling process already had.
//
// Volatility and Unreferenced take no options because they read their whole source
// workspace-wide: the run-history file and the symbol index have no commit window to
// narrow, which is the distinction the lens docs draw for a reader too.
type InsightAnalyzer interface {
	Hotspots(ctx context.Context, opts InsightOptions) (HotspotOutput, error)
	Affinity(ctx context.Context, opts InsightOptions) (AffinityOutput, error)
	Ownership(ctx context.Context, opts InsightOptions) (OwnershipOutput, error)
	Trend(ctx context.Context, opts InsightOptions) (TrendOutput, error)
	Volatility(ctx context.Context) (VolatilityReport, error)
	Unreferenced(ctx context.Context) (UnreferencedOutput, error)
	Duplication(ctx context.Context) (DuplicationOutput, error)
}
