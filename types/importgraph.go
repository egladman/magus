package types

// ImportGraph is which workspace package imports which, read off the declared SCIP
// indexes of every language they cover. magus.importGraph returns it.
type ImportGraph struct {
	// Indexed is false when no symbol index was read; Packages is then empty because
	// nobody looked, not because nothing imports anything.
	Indexed bool `json:"indexed"`
	// Packages maps each workspace-relative package directory ("." for the root) to the
	// sorted directories it imports, self-imports dropped.
	Packages map[string][]string `json:"packages"`
	// Languages maps every directory Packages names, as a key or an import, to the
	// language of the package it holds. A directory whose language the graph does not
	// know is absent. Empty, never nil.
	Languages map[string]string `json:"languages"`
}
