//go:build !wasm

package std

import (
	"context"
	"fmt"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

// symbolIndexWorkspace is the part of *magus.Magus magus\symbolIndexDigest reads,
// recovered by assertion because std cannot import root magus.
type symbolIndexWorkspace interface {
	KnowledgeGraph(ctx context.Context, refresh bool) (*knowledge.Graph, error)
	SymbolGaps(ctx context.Context) ([]types.KnowledgeSymbolGap, bool)
	CacheDir() string
}

// MagusSymbolIndexDigest backs magus\symbolIndexDigest: a digest of the symbol index the
// graph members load, which a target writes as an output for index readers to key their
// cache on. It builds the graph first, so the digest covers the index files on disk now.
// It raises when the gap probe cannot run, since the digest would then claim completeness.
func MagusSymbolIndexDigest(ctx context.Context) (types.SymbolIndexDigest, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return types.SymbolIndexDigest{}, errNoWorkspace("symbolIndexDigest")
	}
	w, ok := ws.(symbolIndexWorkspace)
	if !ok {
		return types.SymbolIndexDigest{}, fmt.Errorf("magus\\symbolIndexDigest: this workspace has no knowledge graph")
	}
	if _, err := w.KnowledgeGraph(ctx, false); err != nil {
		return types.SymbolIndexDigest{}, err
	}
	d, err := knowledge.NewStore(w.CacheDir(), true, 0, nil, nil).SymbolIndexDigest()
	if err != nil {
		return types.SymbolIndexDigest{}, err
	}
	gaps, probed := w.SymbolGaps(ctx)
	if !probed {
		return types.SymbolIndexDigest{}, fmt.Errorf("magus\\symbolIndexDigest: could not probe which declared symbol indexes are missing")
	}
	d.Gaps = gaps
	return d, nil
}
