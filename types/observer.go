package types

import "time"

// Observer receives structured events from a Graph.
// Implementations must be safe for concurrent calls.
type Observer interface {
	OnBuild(BuildStats)
	OnQuery(QueryEvent)
	OnError(error)
}

// BuildStats is emitted once per successful dependency-graph build.
type BuildStats struct {
	Nodes    int
	Edges    int
	Duration time.Duration
}

// QueryEvent is emitted once per top-level query method.
type QueryEvent struct {
	Op          string
	Nodes       int
	Seeds       int
	Strategy    string
	ResultCount int
	Duration    time.Duration
}

// NoopObserver discards every event.
type NoopObserver struct{}

func (NoopObserver) OnBuild(BuildStats) {}
func (NoopObserver) OnQuery(QueryEvent) {}
func (NoopObserver) OnError(error)      {}

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

func (f fanOut) OnBuild(s BuildStats) {
	for _, o := range f {
		o.OnBuild(s)
	}
}

func (f fanOut) OnQuery(e QueryEvent) {
	for _, o := range f {
		o.OnQuery(e)
	}
}

func (f fanOut) OnError(err error) {
	for _, o := range f {
		o.OnError(err)
	}
}
