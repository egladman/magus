package types

// GraphRequest says which knowledge graph a caller needs. The zero value is the warm,
// symbol-free graph every default read answers from.
type GraphRequest struct {
	// Refresh rebuilds the graph instead of reusing a cached build.
	Refresh bool
	// Symbols picks which of the lazily loaded @symbols shards the graph includes.
	Symbols SymbolScope
}

// SymbolScope names the @symbols shards a graph request loads. The symbol set can be
// huge, so a request that includes any of it gets a fresh graph rather than the shared
// warm one. Compare scopes with ==.
type SymbolScope struct {
	all bool
	ref string
}

var (
	// NoSymbols loads no symbol shards.
	NoSymbols = SymbolScope{}
	// AllSymbols loads every symbol shard.
	AllSymbols = SymbolScope{all: true}
)

// SymbolsMentioning loads the shards that mention ref when it is an exact symbol ID or a name
// defined in the workspace, and every shard for a fuzzy name. An empty ref is AllSymbols.
func SymbolsMentioning(ref string) SymbolScope {
	if ref == "" {
		return AllSymbols
	}
	return SymbolScope{ref: ref}
}

// Loaded reports whether the scope includes any symbol shards.
func (s SymbolScope) Loaded() bool { return s.all || s.ref != "" }

// Ref is the symbol the scope targets, or empty for NoSymbols and AllSymbols.
func (s SymbolScope) Ref() string { return s.ref }
