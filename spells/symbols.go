package spells

// SymbolFormat names the code-index format a spell's symbol indexer writes.
//
// It is declared rather than inferred because the format decides which reader
// ingestion picks. Before it existed the contract was implicitly "SCIP protobuf": a
// spell could not say it emitted anything else, and nothing checked that it emitted
// SCIP, so a second format was unexpressible rather than merely unsupported.
type SymbolFormat string

const (
	// SymbolFormatNone is the zero value: the spell declared no format. Not a default
	// for SCIP; declaring the capability without naming its format is rejected at
	// decode, because guessing which format a command emits is the guess this type
	// was added to stop.
	SymbolFormatNone SymbolFormat = ""
	// SymbolFormatSCIP is SCIP protobuf (Sourcegraph's code-index format), the only
	// format magus ingests today.
	SymbolFormatSCIP SymbolFormat = "scip"
)

// SymbolIndexer is what mgs_getSymbolIndexer declares: a spell's symbol-indexing
// capability, as a named export beside mgs_getLanguage rather than a reserved op
// name hidden in mgs_listTargets. Exporting it IS the capability; a spell that does
// not export it is not symbol-capable.
//
// Command writes the index to the destination magus injects as MAGUS_SYMBOL_INDEX
// (see internal/symbols), so the index lands in the cache and the working tree stays
// clean. The command is straight-line data like any op command: it is charm-patched,
// rendered by `magus describe`, and keyed into the cache without being executed.
type SymbolIndexer struct {
	Format  SymbolFormat `json:"format,omitempty"`
	Command Command      `json:"command,omitempty"`
}

// SymbolIndexOp is the op name magus registers a declared indexer under, so an index
// run reaches the cache, keying and freshness machinery as an ordinary command op.
//
// magus owns this name now; a spell no longer spells it. It stays "scip" because
// renaming it would rekey every cached index and rename a target users and docs
// already name, buying nothing: the format a spell emits is declared on
// SymbolIndexer.Format, which is the conflation that mattered.
const SymbolIndexOp = "scip"
