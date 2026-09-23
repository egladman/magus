package mergequeue

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// Event kinds.
const (
	EventPartition = "partition" // planning grouped Changes into Partition
	EventDecided   = "decided"   // planning or validation settled Change
	EventGate      = "gate"      // a gate started on Change's stage Commit at Depth
	EventMerged    = "merged"    // landing merged Change at Commit
	EventKicked    = "kicked"    // landing kicked Change back
	EventWaiting   = "waiting"   // landing left Change queued for a later run
	EventNotice    = "notice"    // anything else worth a line, in Reason
)

// Event is one JSONL record. Every command reports through these alone, one per line.
type Event struct {
	Schema     string    `json:"schema"`
	Time       time.Time `json:"time"`
	Event      string    `json:"event"`
	Change     string    `json:"change,omitempty"`
	Partition  *int      `json:"partition,omitempty"`
	Changes    []string  `json:"changes,omitempty"`
	Decision   Decision  `json:"decision,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	Commit     string    `json:"commit,omitempty"`
	Depth      int       `json:"depth,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
}

// Events writes [Event] records as JSONL. The zero value and a nil *Events discard.
// Safe for concurrent use.
type Events struct {
	mu  sync.Mutex
	w   io.Writer
	now func() time.Time
}

// NewEvents writes to w.
func NewEvents(w io.Writer) *Events { return &Events{w: w, now: time.Now} }

// Emit writes e, stamping its schema and time.
func (e *Events) Emit(ev Event) {
	if e == nil || e.w == nil {
		return
	}
	ev.Schema = SchemaEvent
	if e.now != nil {
		ev.Time = e.now().UTC()
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, _ = e.w.Write(append(line, '\n'))
}

func partitionOf(i int) *int { return &i }
