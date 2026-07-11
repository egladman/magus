package types

import "time"

// InsightDefinition is the umbrella description shown by `magus insight`.
const InsightDefinition = "Insight shows where a codebase's attention and risk concentrate. " +
	"Four lenses read VCS history: hotspots (churn x complexity, the prime refactoring targets), " +
	"affinity (projects that change together, and whether a dependency edge explains it), " +
	"ownership (author concentration and bus factor), and trend (rising vs cooling activity). " +
	"A fifth lens, volatility, reads run-outcome history instead: targets whose pass/fail record " +
	"flaps (a Wilson-scored flakiness signal)."

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
		"complexity — the prime refactoring targets: code both churned often and hard to understand."
	AffinityDefinition = "Affinity is how often projects change in the same commit (temporal " +
		"coupling). A hidden pair has affinity without either declaring a dependency on the other " +
		"— a candidate architectural smell."
	OwnershipDefinition = "Ownership shows author concentration: who touches each project most, " +
		"how many distinct authors it has (bus factor), and whether it has gone quiet (abandonment risk)."
	TrendDefinition = "Trend compares the recent and earlier halves of the window: a positive " +
		"delta is a rising hotspot (accelerating activity), a negative one is cooling."
	VolatilityDefinition = "Volatility reads run-outcome history, not git: each (project, target) " +
		"pair's recent pass/fail/volatile record scored by its Wilson lower bound. A pair at or above " +
		"the configured threshold is flagged volatile - a flakiness signal, the prime stabilization targets."
)

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
	B      string `json:"b"      yaml:"b"`
	Count  int    `json:"count"  yaml:"count"`
	Hidden bool   `json:"hidden,omitempty" yaml:"hidden,omitempty"`
}

// OwnershipOutput reports author concentration per project — the knowledge-risk view.
type OwnershipOutput struct {
	Definition string      `json:"definition" yaml:"definition"`
	Commits    int         `json:"commits"    yaml:"commits"`
	Since      string      `json:"since,omitempty" yaml:"since,omitempty"`
	Projects   []Ownership `json:"projects"   yaml:"projects"`
}

// Ownership is one project's authorship: how many distinct authors touched it, who
// touched it most (and their share), whether it is bus-factor-1 (a single author),
// and whether it has gone quiet in the recent half of the window (abandonment risk).
type Ownership struct {
	Path         string    `json:"path"                   yaml:"path"`
	Commits      int       `json:"commits"                yaml:"commits"`
	Authors      int       `json:"authors"                yaml:"authors"`
	Primary      string    `json:"primary"                yaml:"primary"`
	PrimaryShare int       `json:"primary_share"          yaml:"primary_share"` // percent
	BusFactor1   bool      `json:"bus_factor_1,omitempty" yaml:"bus_factor_1,omitempty"`
	Stale        bool      `json:"stale,omitempty"        yaml:"stale,omitempty"`
	LastCommit   time.Time `json:"last_commit,omitempty"  yaml:"last_commit,omitempty"`
}

// TrendOutput ranks projects by whether their activity is rising or cooling — the
// window is split at its midpoint and the two halves compared.
type TrendOutput struct {
	Definition string  `json:"definition" yaml:"definition"`
	Commits    int     `json:"commits"    yaml:"commits"`
	Since      string  `json:"since,omitempty" yaml:"since,omitempty"`
	Projects   []Trend `json:"projects"   yaml:"projects"`
}

// Trend is one project's churn split across the window's two halves; Delta>0 is rising.
type Trend struct {
	Path    string `json:"path"    yaml:"path"`
	Recent  int    `json:"recent"  yaml:"recent"`
	Earlier int    `json:"earlier" yaml:"earlier"`
	Delta   int    `json:"delta"   yaml:"delta"`
}

// InsightView bundles the four VCS-history lenses plus the run-outcome volatility lens,
// without the knowledge-graph axis. It is what the console serves at GET /api/v1/insight:
// the same per-lens outputs the CLI produces. The four git lenses come from one bounded
// git-log scan (cached by the service); Volatility is a fresh runtime-history file read
// folded into the same response, so the dashboard reads one endpoint for every lens.
// GraphStats is omitted deliberately - the console read never touches the knowledge graph.
type InsightView struct {
	Hotspots   HotspotOutput     `json:"hotspots"   yaml:"hotspots"`
	Affinity   AffinityOutput    `json:"affinity"   yaml:"affinity"`
	Ownership  OwnershipOutput   `json:"ownership"  yaml:"ownership"`
	Trend      TrendOutput       `json:"trend"      yaml:"trend"`
	Volatility *VolatilityReport `json:"volatility" yaml:"volatility"`
}

// InsightReport bundles every lens for the combined `magus insight report` (the
// committable Markdown doc and its -o json form). GraphStats is the knowledge-
// graph axis (`magus graph stats`), embedded so the report spans both axes.
type InsightReport struct {
	Hotspots   HotspotOutput     `json:"hotspots"             yaml:"hotspots"`
	Affinity   AffinityOutput    `json:"affinity"             yaml:"affinity"`
	Ownership  OwnershipOutput   `json:"ownership"            yaml:"ownership"`
	Trend      TrendOutput       `json:"trend"                yaml:"trend"`
	Volatility *VolatilityReport `json:"volatility,omitempty" yaml:"volatility,omitempty"`
	GraphStats KnowledgeStats    `json:"graph_stats"          yaml:"graph_stats"`
}
