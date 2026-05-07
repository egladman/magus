package graph

import "time"

// Observer receives structured events from a Graph; implementations must be concurrency-safe.
type Observer interface {
	OnBuild(BuildStats)
	OnQuery(QueryEvent)
	OnError(error)
}

// BuildStats is emitted once per successful Builder.Build.
type BuildStats struct {
	Nodes    int
	Edges    int
	Duration time.Duration
}

// QueryEvent is emitted once per top-level query method.
type QueryEvent struct {
	Op          string // method name
	Nodes       int    // |V|
	Seeds       int    // seed-set size, or 0 when N/A
	Strategy    string // "bfs" or "bitset"
	ResultCount int    // closure size, path count, or pair count
	Duration    time.Duration
}

// NoopObserver discards every event.
type NoopObserver struct{}

func (NoopObserver) OnBuild(BuildStats) {}
func (NoopObserver) OnQuery(QueryEvent) {}
func (NoopObserver) OnError(error)      {}

// FanOut returns an Observer that forwards calls to every non-nil obs.
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
