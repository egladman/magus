package mergequeue

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// EventKind names what an [Event] reports.
type EventKind string

const (
	EventPartition EventKind = "partition" // planning grouped Changes into Partition
	EventDecided   EventKind = "decided"   // planning or validation settled Change
	EventGate      EventKind = "gate"      // a gate started on Change's candidate Commit at Depth
	EventMerged    EventKind = "merged"    // an Applier merged Change at Commit
	EventKicked    EventKind = "kicked"    // an Applier kicked Change back
	EventWaiting   EventKind = "waiting"   // an Applier left Change queued for a later run
	EventNotice    EventKind = "notice"    // anything else worth a line, in Reason
)

// Event is one JSONL record. Every command reports through these alone, one per line.
type Event struct {
	Schema     string    `json:"schema"`
	Time       time.Time `json:"time"`
	Kind       EventKind `json:"kind"`
	Change     string    `json:"change,omitempty"`
	Partition  *int      `json:"partition,omitempty"`
	Changes    []string  `json:"changes,omitempty"`
	Decision   Decision  `json:"decision,omitempty"`
	Code       Code      `json:"code,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	Commit     string    `json:"commit,omitempty"`
	Depth      int       `json:"depth,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
}

// Events writes [Event] records as JSONL. A nil *Events discards. Safe for concurrent
// use.
type Events struct {
	mu sync.Mutex
	w  io.Writer
}

// NewEvents writes to w.
func NewEvents(w io.Writer) *Events { return &Events{w: w} }

// Emit writes ev, stamping its schema and time. A write error is dropped: events report
// the run and never steer it.
func (e *Events) Emit(ev Event) {
	if e == nil || e.w == nil {
		return
	}
	ev.Schema = SchemaEvent
	ev.Time = time.Now().UTC()
	line, err := json.Marshal(ev)
	if err != nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, _ = e.w.Write(append(line, '\n'))
}

func partitionOf(i int) *int { return &i }
