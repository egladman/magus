package guard

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

// guardLookupBudget bounds every graph-backed guard answer. The hook runs before every
// agent command under a host timeout that fails open, so an answer that arrives late is
// worth less than none: past the budget the lookup is non-definitive and the rule stays
// silent.
const guardLookupBudget = 150 * time.Millisecond

// withinBudget runs lookup and gives up on it after budget. The lookup cannot be
// cancelled (it stats the tree), so it is left to finish on its own goroutine, which the
// hook process's exit reclaims.
func withinBudget[T any](budget time.Duration, lookup func() T) (T, bool) {
	done := make(chan T, 1)
	go func() { done <- lookup() }()
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case v := <-done:
		return v, true
	case <-timer.C:
		var zero T
		return zero, false
	}
}

// guardLookups answers the guard's questions about one workspace's graph index: the
// workspace found from rootOverride, the --root a command loaded as given, or from the cwd
// when it is "".
type guardLookups struct {
	env          HookEnv
	rootOverride string
	// index is the index `magus graph build` wrote for the workspace, read on the first
	// question, or nil when there is none. The graph itself is never loaded here: that
	// costs seconds, and the index answers the guard's questions from one file.
	index func() *knowledge.GuardIndex
}

func (e HookEnv) newLookups(rootOverride string) guardLookups {
	return guardLookups{env: e, rootOverride: rootOverride, index: sync.OnceValue(func() *knowledge.GuardIndex {
		root, err := e.FindRoot(rootOverride)
		if err != nil {
			return nil
		}
		cacheDir, err := e.LoadedCacheDir(root)
		if err != nil {
			return nil
		}
		idx, err := knowledge.ReadGuardIndex(cacheDir, root)
		if err != nil {
			return nil
		}
		return idx
	})}
}

// symbolDefined answers the one question that lets the guard deny a symbol search: does
// refs return the same sites this grep is reaching for. Definitive only when the index's
// symbols were fresh when it was written and no source has moved since; a deny resting on
// anything less would take grep away on a lookup that may be wrong.
func (l guardLookups) symbolDefined(ident string) (defined, definitive bool) {
	type answer struct{ defined, definitive bool }
	a, ok := withinBudget(guardLookupBudget, func() answer {
		idx := l.index()
		if idx == nil {
			return answer{}
		}
		return answer{idx.Has(knowledge.GuardSymbol, ident), idx.Fresh(knowledge.GuardSymbol)}
	})
	return a.defined, ok && a.definitive
}

// symbolIDs lists the ids of the symbols named ident, none past the lookup budget.
func (l guardLookups) symbolIDs(ident string) []string {
	ids, _ := withinBudget(guardLookupBudget, func() []string {
		if idx := l.index(); idx != nil {
			return idx.SymbolIDs(ident)
		}
		return nil
	})
	return ids
}

// symbolSites reads ident's sites from the refs file beside the guard index, on the same
// freshness terms as symbolDefined.
func (l guardLookups) symbolSites(ident string) ([]types.KnowledgeRefSite, bool) {
	type answer struct {
		sites      []types.KnowledgeRefSite
		definitive bool
	}
	a, ok := withinBudget(guardLookupBudget, func() answer {
		idx := l.index()
		if idx == nil {
			return answer{}
		}
		sites, err := idx.RefSites(ident)
		if err != nil {
			return answer{}
		}
		return answer{sites, idx.Fresh(knowledge.GuardSymbol)}
	})
	return a.sites, ok && a.definitive
}

// graphIDs lists kind's ids from the guard index, definitive only while the sources they
// came from are unchanged.
func (l guardLookups) graphIDs(_ context.Context, kind string) ([]string, bool) {
	type answer struct {
		ids   []string
		fresh bool
	}
	a, ok := withinBudget(guardLookupBudget, func() answer {
		idx := l.index()
		if idx == nil {
			return answer{}
		}
		return answer{idx.IDs(kind), idx.Fresh(kind)}
	})
	if !ok || !a.fresh {
		return nil, false
	}
	return a.ids, true
}

// indexedIDs lists kind's ids from the guard index whether or not its sources have moved
// since; the rules that call it prove their answer against the disk.
func (l guardLookups) indexedIDs(_ context.Context, kind string) ([]string, bool) {
	type answer struct {
		ids   []string
		found bool
	}
	a, ok := withinBudget(guardLookupBudget, func() answer {
		idx := l.index()
		if idx == nil {
			return answer{}
		}
		return answer{idx.IDs(kind), true}
	})
	return a.ids, ok && a.found
}

// staleGraphAdvice is what the guard says to a graph read about to answer from an index
// older than the tree, or "" when every built index is current.
func (l guardLookups) staleGraphAdvice(ctx context.Context) string {
	reason, _ := withinBudget(guardLookupBudget, func() string {
		if idx := l.index(); idx != nil {
			return idx.Stale()
		}
		return ""
	})
	advice := knowledge.RenderStaleGraphAdvice(reason, l.env.StaleIndexes(ctx, l.rootOverride))
	if advice == "" {
		return ""
	}
	if cause := l.indexCause(); cause != "" {
		advice += "\n" + cause
	}
	return advice
}

// indexCause is why the workspace's index is missing or behind, as advisory sentences, or
// "" when it does not answer within the guard's lookup budget.
func (l guardLookups) indexCause() string {
	root, err := l.env.FindRoot(l.rootOverride)
	if err != nil {
		return ""
	}
	c, ok := withinBudget(guardLookupBudget, func() types.KnowledgeIndexCause {
		return l.env.IndexCause(context.Background(), root)
	})
	if !ok {
		return ""
	}
	return fmt.Sprintf("Why: %s. Fix: %s.", c.Why, c.Fix)
}
