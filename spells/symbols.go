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
//
// Uses names the spell's own tools the indexer runs besides its binary: scip-go loads
// packages through `go`, so a Go index is out of date once the toolchain moves. Each entry
// must be a tool mgs_getTools declares with a version probe; that version keys the
// indexer's op and no other target.
//
// Op names the op magus registers the indexer under; empty means DefaultSymbolIndexOp.
// A spell whose index rides beside another spell's on one project declares an op of its
// own, so each index runs, keys and fails apart. Two spells on one project that run
// under the same op share its one index, and the first one bound names its language.
type SymbolIndexer struct {
	Format  SymbolFormat `json:"format,omitempty"`
	Op      string       `json:"op,omitempty"`
	Command Command      `json:"command,omitempty"`
	Uses    []string     `json:"uses,omitempty"`
}

// DefaultSymbolIndexOp is the op a symbol indexer that declares none runs under.
// Registering the indexer as an op is what lets an index run reach the cache, keying and
// freshness machinery as an ordinary command op. It stays "scip" because renaming it
// would re-key every cached index and rename a target users and docs already name.
const DefaultSymbolIndexOp = "scip"

// OpName returns the op si runs under: its declared Op, else DefaultSymbolIndexOp, and ""
// for a nil si. Decode registers the indexer under this name, and the run, freshness and
// ingestion paths all find the index through it, so there is one answer to which op
// writes a spell's index.
func (si *SymbolIndexer) OpName() string {
	if si == nil {
		return ""
	}
	if si.Op != "" {
		return si.Op
	}
	return DefaultSymbolIndexOp
}

// SymbolIndexOp returns the op s's symbol indexer runs under, or "" when s declares none.
func (s *Spell) SymbolIndexOp() string { return s.symbolIndexer.OpName() }
