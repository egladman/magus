package types

// The impact report, as a domain type rather than an internal one.
//
// magus.affectedImpact returns ImpactResult, so a caller reads the affected set and why
// each project is in it as values. project/impact aliases these back, so the analysis
// there keeps its own vocabulary.

// ImpactCoverage is a covered/total statement tally and its ratio (0..1), mirrored from the
// knowledge graph's @coverage overlay so the impact report carries the raw counts.
type ImpactCoverage struct {
	Ratio   float64 `json:"ratio"         yaml:"ratio"`
	Covered int     `json:"covered_stmts" yaml:"covered_stmts"`
	Total   int     `json:"total_stmts"   yaml:"total_stmts"`
}

// ImpactSymbol is one changed symbol's caller spread (and coverage, when observed): the
// file that defines it, its identity, how many references and distinct referencing files
// the symbol index recorded, and its covered-statement ratio if a coverage profile is
// loaded.
type ImpactSymbol struct {
	File      string          `json:"file"               yaml:"file"`
	Symbol    string          `json:"symbol"             yaml:"symbol"`
	Label     string          `json:"label,omitempty"    yaml:"label,omitempty"`
	RefCount  int             `json:"ref_count"          yaml:"ref_count"`
	FileCount int             `json:"file_count"         yaml:"file_count"`
	Coverage  *ImpactCoverage `json:"coverage,omitempty" yaml:"coverage,omitempty"`
}

// ImpactFileCoverage is one changed file's observed file-level coverage.
type ImpactFileCoverage struct {
	File     string         `json:"file"     yaml:"file"`
	Coverage ImpactCoverage `json:"coverage" yaml:"coverage"`
}

// ImpactProject is one project in the blast radius.
type ImpactProject struct {
	Path string `json:"path" yaml:"path"`
	// Seed is true when a changed file lands directly in this project (it is a root
	// of the closure, not only reached transitively).
	Seed bool `json:"seed" yaml:"seed"`
	// Files are the changed files inside this project, present only for seeds.
	Files []string `json:"files,omitempty" yaml:"files,omitempty"`
	// Spells are the project's bound spells (its toolchains).
	Spells []string `json:"spells,omitempty" yaml:"spells,omitempty"`
	// Targets is the project's target vocabulary: the spell-contributed ops plus any
	// custom magusfile targets that name it, sorted and deduplicated.
	Targets []string `json:"targets,omitempty" yaml:"targets,omitempty"`
}

// ImpactResult is the typed impact report for a changeset. Counts sit alongside the
// backing lists so a formatter can lead with the count and expand the detail
// (the `magus graph explain` house style).
type ImpactResult struct {
	// Base is the ref the VCS diff was taken against ("paths" when computed from an
	// explicit path set rather than a diff).
	Base string `json:"base" yaml:"base"`
	// ChangedFileCount and ChangedFiles are the full changed-file set. Files outside
	// any project still count here (they just seed nothing).
	ChangedFileCount int      `json:"changed_file_count"      yaml:"changed_file_count"`
	ChangedFiles     []string `json:"changed_files,omitempty" yaml:"changed_files,omitempty"`
	// SeedProjects are the projects that directly contain a changed file, sorted.
	SeedProjects []string `json:"seed_projects,omitempty" yaml:"seed_projects,omitempty"`
	// AffectedProjects is the transitive reverse closure of the seeds (seeds
	// included), sorted by path. Each carries its target vocabulary and whether a
	// changed file lands in it directly.
	AffectedProjects []ImpactProject `json:"affected_projects,omitempty" yaml:"affected_projects,omitempty"`
	// ChangedSymbols is the changed-symbol caller overlay: every symbol defined in a
	// changed source file, with how widely it is referenced repo-wide. It is what a
	// plain difftool structurally cannot show - the reach of an edited definition.
	// Populated by Enrich when a symbol index is loaded; empty (with a Note) otherwise.
	// Flattened across files and sorted by descending reference count so the
	// widest-reach change leads.
	ChangedSymbols []ImpactSymbol `json:"changed_symbols,omitempty" yaml:"changed_symbols,omitempty"`
	// ChangedFileCoverage is the coverage overlay: the observed statement coverage of
	// each changed file the local coverage profile covers. Empty (with a Note) when no
	// `magus run coverage` profile is loaded. Go-only and observed, never extracted.
	ChangedFileCoverage []ImpactFileCoverage `json:"changed_file_coverage,omitempty" yaml:"changed_file_coverage,omitempty"`
	// Notes carries graceful-degradation messages (deferred overlays, missing data).
	// It never blocks a report; a formatter prints it verbatim.
	Notes []string `json:"notes,omitempty" yaml:"notes,omitempty"`
}
