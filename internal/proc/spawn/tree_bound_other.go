//go:build !linux && !darwin

package spawn

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
)

// boundedTree is inert here: this host has no process table magus reads.
type boundedTree struct{}

type treeBounds struct{ warned sync.Once }

var processBounds = &treeBounds{}

// ErrTooManyProcesses is a command whose process tree passed its bound.
var ErrTooManyProcesses = errors.New("process tree over its bound")

// follow says once that nothing bounds the tree. Windows is quiet: taskkill /T
// already ends the tree, and wasm runs no process.
func (b *treeBounds) follow(int, string) *boundedTree {
	if runtime.GOOS == "windows" || runtime.GOARCH == "wasm" {
		return nil
	}
	b.warned.Do(func() {
		slog.Warn(fmt.Sprintf("magus bounds and reaps a command's process tree only on linux and darwin, not %s", runtime.GOOS))
	})
	return nil
}

func (*treeBounds) killTree(*boundedTree)    {}
func (*treeBounds) stop(*boundedTree) error { return nil }
