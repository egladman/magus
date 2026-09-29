package types

// ImportGraph is which workspace package imports which, read off the declared SCIP
// indexes. magus.importGraph returns it.
type ImportGraph struct {
	// Indexed is false when no symbol index was read; Packages is then empty because
	// nobody looked, not because nothing imports anything.
	Indexed bool `json:"indexed"`
	// Packages maps each workspace-relative package directory ("." for the root) to the
	// sorted directories it imports, self-imports dropped.
	Packages map[string][]string `json:"packages"`
}
