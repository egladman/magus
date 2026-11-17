package trail

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/egladman/magus/libs/codec"
	trailtypes "github.com/egladman/magus/libs/safetyscissors/trail"
)

// storedEvent is a trail event as magus decodes it from its own activity trail: the
// module's [trailtypes.Event], plus the duration spelling magus wrote before it.
type storedEvent struct {
	trailtypes.Event
}

// UnmarshalJSON decodes an event, reading a duration written under either spelling.
//
// compat(until: no activity trail still holds "dur_ms"): magus spelled the duration `dur_ms`
// before [trailtypes.Event.DurationMs] was. The trail is append-only and rotates, so
// observing that dropping this is safe means finding none left:
//
//	grep -l '"dur_ms"' <base>/activity/events.jsonl
//
// Dropping it early is silent rather than loud: the server's scheduler reads a job's last
// finish as start plus duration, so every pre-rename row reads as having finished the instant
// it started, and a job whose interval has not elapsed is rerun anyway.
func (e *storedEvent) UnmarshalJSON(b []byte) error {
	if err := codec.Unmarshal(b, &e.Event); err != nil {
		return err
	}
	if e.DurationMs == 0 {
		var legacy struct {
			DurMs int64 `json:"dur_ms"`
		}
		if codec.Unmarshal(b, &legacy) == nil {
			e.DurationMs = legacy.DurMs
		}
	}
	return nil
}

// readRecent is [trailtypes.ReadRecent] decoding each line as a [storedEvent]. It is a copy
// because the module's reader decodes only the spelling the module writes, and the older one
// is magus's to read.
func readRecent(ctx context.Context, base string, limit int) ([]trailtypes.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if base == "" || limit <= 0 {
		return nil, &trailtypes.InvalidReadError{Dir: base, Limit: limit}
	}
	f, err := os.Open(eventsPath(base))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []trailtypes.Event{}, nil
		}
		return nil, fmt.Errorf("trail: open %s: %w", eventsPath(base), err)
	}
	defer func() { _ = f.Close() }() // read only: a failed close loses nothing read

	// A circular buffer over the tail; head marks the oldest slot, so no line is shifted.
	buf := make([]string, 0, limit)
	head := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := sc.Text()
		if line == "" {
			continue
		}
		if len(buf) < limit {
			buf = append(buf, line)
		} else {
			buf[head] = line
			head = (head + 1) % limit
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("trail: read %s: %w", eventsPath(base), err)
	}

	out := make([]trailtypes.Event, 0, len(buf))
	for i := len(buf) - 1; i >= 0; i-- { // newest first
		var e storedEvent
		if codec.Unmarshal([]byte(buf[(head+i)%len(buf)]), &e) == nil {
			out = append(out, e.Event)
		}
	}
	return out, nil
}
