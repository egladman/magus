package dashboard

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/file/watch"
)

// WatchInvalidate starts a goroutine that reads from w.Events() and writes to
// the returned channel whenever a graph-relevant file change is batched. The
// channel has capacity 1; a non-blocking send is used so a slow consumer never
// blocks the watcher goroutine. The goroutine exits when ctx is cancelled or
// the watcher closes its Events() channel.
//
// Callers pass the returned channel as Options.GraphInvalidate. The channel
// is never closed by this package; it is garbage-collected when the context is
// cancelled and no more references to it exist.
func WatchInvalidate(ctx context.Context, w *watch.Watcher) <-chan struct{} {
	ch := make(chan struct{}, 1)
	go func() {
		events := w.Events()
		for {
			select {
			case <-ctx.Done():
				return
			case batch, ok := <-events:
				if !ok {
					return
				}
				if isGraphRelevant(batch.Paths) {
					// Non-blocking: if the channel is already full the consumer
					// will still be notified of the already-pending change.
					select {
					case ch <- struct{}{}:
					default:
					}
				}
			}
		}
	}()
	return ch
}

// isGraphRelevant reports whether any changed path feeds the knowledge graph.
// Mirrors magus.graphRelevant (warmgraph.go) without importing the root package
// to avoid an import cycle.
func isGraphRelevant(paths []string) bool {
	for _, p := range paths {
		if strings.HasSuffix(p, ".buzz") || strings.HasSuffix(p, ".md") {
			return true
		}
		switch filepath.Base(p) {
		case "magus.yaml", "magus.yml", "magusfiles":
			return true
		}
	}
	return false
}
