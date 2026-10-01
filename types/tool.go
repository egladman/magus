package types

// ToolReport is every project's tools, their probed versions, their windows, and where each
// probed version's release cycle stands: `magus describe tools` and magus\describe.tool() return it.
// Annotate a Buzz result `> ToolReport` for compile-checked field access.
//
// Field names and values track magus.tool.v1alpha1 (proto/magus/tool/v1alpha1/tool.proto),
// which serves the same view to the console. One concept gets one vocabulary, or a script
// author has to learn which surface they are reading before they can read it.
type ToolReport struct {
	Definition string `json:"definition" yaml:"definition" buzz:"-"`
	Workspace  string `json:"workspace" yaml:"workspace"`
	Count      int    `json:"count" yaml:"count"`
	// Lifecycle says where the rows' cycle, eol and support columns came from.
	Lifecycle LifecycleStatus `json:"lifecycle" yaml:"lifecycle"`
	Tools     []ToolRow       `json:"tools" yaml:"tools"`
}

// ToolRow is one binary a project's spells drive, with the window it is held to.
type ToolRow struct {
	Project string `json:"project" yaml:"project"`
	Bin     string `json:"bin" yaml:"bin"`
	Spell   string `json:"spell" yaml:"spell"`
	// Empty means no version was read; ProbeError says whether the tool could not run or
	// ran and printed nothing version-shaped. Only the first means "not installed".
	InstalledVersion string `json:"installed_version,omitempty" yaml:"installed_version,omitempty"`
	ProbeError       string `json:"probe_error,omitempty" yaml:"probe_error,omitempty"`
	// The two declarations stay separate: the first question about a failing bound is
	// who set it, and the effective window alone cannot say.
	SpellBounds     string `json:"spell_bounds,omitempty" yaml:"spell_bounds,omitempty"`
	WorkspaceBounds string `json:"workspace_bounds,omitempty" yaml:"workspace_bounds,omitempty"`
	Effective       string `json:"effective,omitempty" yaml:"effective,omitempty"`
	Verdict         string `json:"verdict" yaml:"verdict"`
	DiagnosticCode  string `json:"diagnostic_code,omitempty" yaml:"diagnostic_code,omitempty"`
	// Lifecycle is the product the spell names for this binary (spells.Tool.Lifecycle),
	// empty when it names none.
	Lifecycle string `json:"lifecycle,omitempty" yaml:"lifecycle,omitempty"`
	// Cycle is the release line the installed version belongs to, and EOL its end date.
	// Both are empty when nothing matched.
	Cycle string `json:"cycle,omitempty" yaml:"cycle,omitempty"`
	EOL   string `json:"eol,omitempty" yaml:"eol,omitempty"`
	// Support is a spells.Support value: supported, eol, unannounced or unknown. Unknown
	// covers every row the provider could not answer; LifecycleStatus.State says why.
	Support string `json:"support,omitempty" yaml:"support,omitempty"`
}

// The verdicts a ToolRow can carry. Underscored, not spaced, so a shell comparison needs no
// quoting and the values line up with the VERDICT_* enum the console reads.
const (
	ToolVerdictInside     = "inside"
	ToolVerdictTooOld     = "too_old"
	ToolVerdictTooNew     = "too_new"
	ToolVerdictUnknown    = "unknown"    // a bound could not be compared
	ToolVerdictUnreadable = "unreadable" // the probe ran and printed nothing version-shaped
	ToolVerdictUnprobed   = "unprobed"   // the probe could not run: the only "not installed"
)

// LifecycleStatus is where a report's end-of-life data came from, and how fresh it is.
type LifecycleStatus struct {
	// Provider is the spell wired with magus\lifecycle.provider, empty when none is.
	Provider string `json:"provider,omitempty" yaml:"provider,omitempty"`
	// State is one of the Lifecycle* constants.
	State string `json:"state" yaml:"state"`
	// Sources are the URLs the provider read, printed so the network use is visible.
	Sources []string `json:"sources,omitempty" yaml:"sources,omitempty"`
	// AsOf is the OLDEST upstream last-modified among the answers, RFC 3339: the report is
	// no fresher than its stalest source.
	AsOf string `json:"as_of,omitempty" yaml:"as_of,omitempty"`
	// FetchedAt is when magus asked the provider, RFC 3339. On a cached, offline or
	// unreached answer it is the time of the fetch being replayed.
	FetchedAt string `json:"fetched_at,omitempty" yaml:"fetched_at,omitempty"`
	// Detail is why the provider could not be asked or did not answer.
	Detail string `json:"detail,omitempty" yaml:"detail,omitempty"`
}

// The states a LifecycleStatus can carry.
const (
	LifecycleLive      = "live"      // asked the provider on this call
	LifecycleCached    = "cached"    // replayed a stored answer without asking
	LifecycleOffline   = "offline"   // MAGUS_OFFLINE forbade asking; a stored answer, or none
	LifecycleUnreached = "unreached" // the provider was asked and did not answer; a stored answer, or none
	LifecycleUnwired   = "unwired"   // the workspace wires no lifecycle provider
)
