package impact

// Coverage is a covered/total statement tally and its ratio (0..1), mirrored
// from the knowledge graph's @coverage overlay.
type Coverage struct {
	Ratio   float64 `json:"ratio"         yaml:"ratio"`
	Covered int     `json:"covered_stmts" yaml:"covered_stmts"`
	Total   int     `json:"total_stmts"   yaml:"total_stmts"`
}

// Project is one project in a change's blast radius.
type Project struct {
	Path string `json:"path" yaml:"path"`
	// Seed is true when a changed file lands directly in this project, a
	// root of the closure rather than only reached transitively.
	Seed bool `json:"seed" yaml:"seed"`
	// Files are the changed files inside this project, present only for seeds.
	Files []string `json:"files,omitempty" yaml:"files,omitempty"`
	// UndeclaredFiles are the Files no project declares. They seeded this
	// project by directory containment alone (MGS1028).
	UndeclaredFiles []string `json:"undeclared_files,omitempty" yaml:"undeclared_files,omitempty"`
	// Spells are the project's bound spells (its toolchains).
	Spells []string `json:"spells,omitempty" yaml:"spells,omitempty"`
	// Targets is the project's target vocabulary: spell ops plus the custom
	// targets that name it, sorted and deduplicated.
	Targets []string `json:"targets,omitempty" yaml:"targets,omitempty"`
}
