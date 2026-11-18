package viewer

import (
	"context"

	"github.com/egladman/magus/internal/cache"
)

// outputSource is the narrow repository contract the run-browser RPCs need: list the stored run
// descriptors, and read one run's captured bytes by ref. Satisfied by *cache.OutputStore, so the
// handler package never grows its own store logic: it just serves what the store already knows.
type outputSource interface {
	ListDescriptors() ([]cache.OutputDescriptor, error)
	ByRef(ref string) ([]byte, cache.OutputDescriptor, error)
}

// runSource lists the retained run journals and resolves an output ref to the
// run that produced it. Satisfied by *cache.OutputStore; the journals
// themselves are read with the journal package.
type runSource interface {
	ListRunLogs(ctx context.Context, limit int) []cache.RunLog
	DescriptorByRef(ref string) (cache.OutputDescriptor, error)
}
