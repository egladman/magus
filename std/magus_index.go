//go:build !wasm

package std

import (
	"context"
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

// symbolIndexWorkspace is the part of *magus.Magus magus\graph reads,
// recovered by assertion because std cannot import root magus.
type symbolIndexWorkspace interface {
	FreshenSymbolIndexes(ctx context.Context, languages []string) error
	KnowledgeGraph(ctx context.Context, req types.GraphRequest) (*knowledge.Graph, error)
	SymbolGaps(ctx context.Context) ([]types.KnowledgeSymbolGap, bool)
	CacheDir() string
}

// MagusSymbolIndexDigest backs magus\graph: a digest of the symbol index the
// graph members load, which a target writes as an output for index readers to key their
// cache on. It freshens declared indexes first, so a reader never has to depend on a
// workflow-specific graph-build step. It raises when the gap probe cannot run, since the
// digest would then claim completeness.
// languages limits the freshen to those indexes; empty freshens all.
// One evaluation computes the digest once per languages list, so every
// graph read sees the index as the evaluation began, as EvalMemo promises.
func MagusSymbolIndexDigest(ctx context.Context, languages []string) (types.SymbolIndexDigest, error) {
	memo := types.EvalMemoFromContext(ctx)
	if memo == nil {
		return symbolIndexDigest(ctx, languages)
	}
	v, err := memo.Do("symbol-index-digest:"+strings.Join(languages, ","), func() (any, error) {
		return symbolIndexDigest(ctx, languages)
	})
	if err != nil {
		return types.SymbolIndexDigest{}, err
	}
	d, _ := v.(types.SymbolIndexDigest)
	return d, nil
}

func symbolIndexDigest(ctx context.Context, languages []string) (types.SymbolIndexDigest, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return types.SymbolIndexDigest{}, errorNoWorkspace("graph")
	}
	w, ok := ws.(symbolIndexWorkspace)
	if !ok {
		return types.SymbolIndexDigest{}, fmt.Errorf("magus\\graph: this workspace has no knowledge graph")
	}
	if err := w.FreshenSymbolIndexes(ctx, languages); err != nil {
		return types.SymbolIndexDigest{}, err
	}
	if _, err := w.KnowledgeGraph(ctx, types.GraphRequest{}); err != nil {
		return types.SymbolIndexDigest{}, err
	}
	d, err := knowledge.NewStore(w.CacheDir(), true, 0, nil, nil).SymbolIndexDigest()
	if err != nil {
		return types.SymbolIndexDigest{}, err
	}
	gaps, probed := w.SymbolGaps(ctx)
	if !probed {
		return types.SymbolIndexDigest{}, fmt.Errorf("magus\\graph: could not probe which declared symbol indexes are missing")
	}
	d.Gaps = gaps
	d.Languages = languages
	return d, nil
}
