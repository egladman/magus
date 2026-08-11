package types

import "time"

// InsightDefinition is the umbrella description shown by `magus insight`.
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
// GraphStats is omitted deliberately - the console read never touches the knowledge graph.
type InsightView struct {
	Hotspots  HotspotOutput   `json:"hotspots"   yaml:"hotspots"`
	Affinity  AffinityOutput  `json:"affinity"   yaml:"affinity"`
	Ownership OwnershipOutput `json:"ownership"  yaml:"ownership"`
	Trend     TrendOutput     `json:"trend"      yaml:"trend"`
	// Volatility stays a POINTER here while InsightReport's is a value, and the two
	// are not carelessly out of step: this is the console's wire shape, where absent
	// and empty say different things. docs/reference/api/insight.md commits to it -
	// null renders "no runs recorded yet", an empty report renders "no volatile
	// targets" - and volatilityToProto maps nil to a nil message to preserve it.
	// InsightReport has no such reader: it crosses into Buzz, where the mirror
	// declares the field non-optional so a magusfile reaches .volatility.targets
	// without a nil guard.
	Volatility *VolatilityReport `json:"volatility" yaml:"volatility"`
}

// InsightReport bundles every lens for the combined `magus insight report` (the
// committable Markdown doc and its -o json form). GraphStats is the knowledge-
// graph axis (`magus graph stats`), embedded so the report spans both axes.
//
// Volatility is a VALUE, not a pointer, so the Buzz mirror declares it non-optional and
// a caller reads report.volatility.targets without a nil guard. An empty Targets list is
// what "the run-outcome axis had nothing to say" means, which is the same test every
// consumer already made. Deliberately unlike InsightView's pointer above - see the note
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
	GraphStats   KnowledgeStats     `json:"graph_stats"  yaml:"graph_stats"`
}
