package symbols

import (
	"fmt"
	"os"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/egladman/magus/internal/file"
)

// MergeIndexes merges the SCIP indexes at srcs, one per environment the indexer ran in,
// into one index written to dst. It is how a spell's SymbolIndexer.Envs reach the single
// index ingestion reads: each run sees only the files its build configuration compiles,
// so a *_windows.go file exists in the windows run alone.
//
// Documents merge by relative path. A path present in several runs is one file read from
// the same bytes, and a SCIP symbol names a package and an identifier, never the build
// configuration that compiled it, so every run records the same occurrences and symbols
// for it; the first run in srcs keeps its copy, which makes the merge deterministic.
// External symbols merge by symbol, first run wins, for the same reason. Metadata comes
// from the first run; a run rooted elsewhere is an error, since its relative paths would
// name other files.
//
// Readers of dst see the previous index or the merged one, never a partial write.
func MergeIndexes(dst string, srcs []string) error {
	if len(srcs) == 0 {
		return fmt.Errorf("symbols: merge into %s: no indexes", dst)
	}
	var merged *scip.Index
	docs := map[string]bool{}
	externals := map[string]bool{}
	for _, src := range srcs {
		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("symbols: merge: %w", err)
		}
		idx, err := DecodeIndex(data)
		if err != nil {
			return fmt.Errorf("merge %s: %w", src, err)
		}
		if merged == nil {
			merged = &scip.Index{Metadata: idx.GetMetadata()}
		} else if root, want := idx.GetMetadata().GetProjectRoot(), merged.GetMetadata().GetProjectRoot(); root != want {
			return fmt.Errorf("symbols: merge %s: project root %q, want %q", src, root, want)
		}
		for _, doc := range idx.GetDocuments() {
			if !docs[doc.GetRelativePath()] {
				docs[doc.GetRelativePath()] = true
				merged.Documents = append(merged.Documents, doc)
			}
		}
		for _, sym := range idx.GetExternalSymbols() {
			if !externals[sym.GetSymbol()] {
				externals[sym.GetSymbol()] = true
				merged.ExternalSymbols = append(merged.ExternalSymbols, sym)
			}
		}
	}
	data, err := proto.Marshal(merged)
	if err != nil {
		return fmt.Errorf("symbols: merge into %s: %w", dst, err)
	}
	if err := file.ReplaceFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("symbols: merge into %s: %w", dst, err)
	}
	return nil
}
