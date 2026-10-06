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
// must be a tool mgs_getTools declares with a version probe; that version keys the scip op
// and no other target.
type SymbolIndexer struct {
	Format  SymbolFormat `json:"format,omitempty"`
	Command Command      `json:"command,omitempty"`
	Uses    []string     `json:"uses,omitempty"`
}

// SymbolIndexOp is the op name the go, typescript, python and rust indexers run under,
// and the prefix of every other spell's (see SymbolIndexOpFor). Registering the indexer
// as an op is what lets an index run reach the cache, keying and freshness machinery as
// an ordinary command op.
const SymbolIndexOp = "scip"

// SymbolIndexOpFor returns the op name magus registers spell's declared indexer under:
// scip-<spell>, so each indexing spell bound to one project runs, keys, stamps and fails
// on its own. A Buzz index rides beside the Go index of the same project, and sharing one
// op made a missing scip-buzz fail the Go index with it.
//
// magus owns these names; a spell never spells them, and Decode refuses an authored op
// that does.
func SymbolIndexOpFor(spell string) string {
	// compat(until: the cache key schema next changes, which re-runs every index anyway,
	// and `magus query scip` finds no doc or CI workflow naming the bare op): these four
	// indexed under the bare name before a project could hold two indexes, and renaming
	// their op would re-run every cached index and break `magus run ::scip`. Two of them
	// bound to one project still share the op and its index, as they always did.
	switch spell {
	case "go", "typescript", "python", "rust":
		return SymbolIndexOp
	}
	return SymbolIndexOp + "-" + spell
}

// SymbolIndexOp returns the op name s's symbol indexer runs under, or "" when s has none:
// the op of kind OpKindSymbolIndex, which Decode synthesizes from mgs_getSymbolIndexer. A
// spell built in Go with an indexer but no such op (a test fixture) runs it under the bare
// SymbolIndexOp.
func (s *Spell) SymbolIndexOp() string {
	for name, op := range s.ops {
		if op.Kind == OpKindSymbolIndex {
			return name
		}
	}
	if s.symbolIndexer != nil {
		return SymbolIndexOp
	}
	return ""
}
