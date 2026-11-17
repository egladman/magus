package job

// SymbolDelta is what a diff did to the symbols around it, beside what any goal asked of
// them. Added are the exported Go symbols it introduced, qualified `Type#Member` for a
// member; Left are the symbols that lost their last use in it and are still defined, each
// with where it is defined. Both sorted. internal/job/evidence reads one.
type SymbolDelta struct {
	Added, Left []string
}
