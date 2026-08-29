package viewer

import (
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/journal"
)

// outputSource is the narrow repository contract the run-browser RPCs need: list the stored run
// descriptors, and read one run's captured bytes by ref. Satisfied by *cache.OutputStore, so the
// handler package never grows its own store logic - it just serves what the store already knows.
type outputSource interface {
	ListDescriptors() []cache.OutputDescriptor
	ByRef(ref string) ([]byte, cache.OutputDescriptor, error)
}

// runSource is the narrow repository contract the invocation RPCs need: list the retained run
// journals, read one back as events, tail one that is still being written, and resolve an output
// ref to the run that produced it. Satisfied by *cache.OutputStore, like [outputSource] beside it.
type runSource interface {
	ListRunLogs(limit int) []cache.RunLog
	InvocationEventsByID(inv string) (journal.Invocation, []journal.Event, error)
	InvocationEventsFrom(inv string, from int64) ([]journal.Event, int64, error)
	DescriptorByRef(ref string) (cache.OutputDescriptor, error)
}
