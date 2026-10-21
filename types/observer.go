package types

import "time"

// Observer receives one event per dependency-graph build and per top-level query.
// Implementations must be safe for concurrent calls.
type Observer interface {
	Observe(GraphEvent)
}

// GraphOpBuild is GraphEvent.Op for a graph build; every other Op names a query method.
const GraphOpBuild = "build"

// GraphEvent is one dependency-graph build or top-level query. Err is set when it failed:
// a build that found a cycle, or a query whose context ended before it finished.
type GraphEvent struct {
	Op          string
	Nodes       int           // |V|
	Edges       int           // |E|, builds only
	Seeds       int           // seed-set size, or 0 when N/A
	Strategy    string        // "bfs" or "bitset", or empty
	ResultCount int           // closure size, path count, or pair count
	Duration    time.Duration // zero for a failure
	Err         error
}

// NoopObserver discards every event.
type NoopObserver struct{}

// Observe drops the event, so a caller with no interest in events need not nil-check an Observer.
func (NoopObserver) Observe(GraphEvent) {}

// Direction is the traversal direction for dependency-graph rendering.
type Direction int

const (
	Downstream Direction = iota // dependencies of each project
	Upstream                    // dependents of each project
)

// FanOut returns an Observer forwarding to every non-nil observer.
func FanOut(obs ...Observer) Observer {
	live := make([]Observer, 0, len(obs))
	for _, o := range obs {
		if o != nil {
			live = append(live, o)
		}
	}
	switch len(live) {
	case 0:
		return NoopObserver{}
	case 1:
		return live[0]
	default:
		return fanOut(live)
	}
}

type fanOut []Observer

func (f fanOut) Observe(e GraphEvent) {
	for _, o := range f {
		o.Observe(e)
	}
}
