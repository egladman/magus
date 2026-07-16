package dependency

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
