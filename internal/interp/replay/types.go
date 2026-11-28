package replay

import (
	"errors"
	"fmt"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/types/enum"
)

// format is the format a replay log's header names on its first line.
const format ="magus.replay/v1alpha1"

// callEnd is how a recorded call ended. A call with no end line on disk reads
// back as [callOpen]. Its process died inside it, so its effect is unknown.
type callEnd string

const (
	// callOpen is a call that began and has no recorded end.
	callOpen callEnd = "open"
	// callReturned holds its value in Result, or in Unencodable why the record could
	// not.
	callReturned callEnd = "returned"
	// callRaised is a call whose error reached the script; its Result is empty.
	callRaised callEnd = "raised"
)

var callEnds = enum.Set[callEnd]{callOpen, callReturned, callRaised}

// Values lists the ends a line may name.
func (v callEnd) Values() []string { return callEnds.Strings() }

// Valid reports whether v is one of the ends.
func (v callEnd) Valid() bool { return callEnds.Valid(v) }

// Refusal is why a recorded run cannot be resumed from its record. Every refusal
// has the same remedy, running the script from the top.
type Refusal string

const (
	// RefusalNoRecord is a run with no record to resume from.
	RefusalNoRecord Refusal = "no-record"
	// RefusalImportChanged is a run whose imported file's digest differs from the record's.
	RefusalImportChanged Refusal = "import-changed"
	// RefusalDiverged is a live call that differs from the next recorded one in name or
	// arguments.
	RefusalDiverged Refusal = "diverged"
	// RefusalLive is a recorded call to a module whose results name state in the process
	// that made them.
	RefusalLive Refusal = "live"
	// RefusalUnencodable is a recorded call whose result the record could not hold.
	RefusalUnencodable Refusal = "unencodable"
	// RefusalOutcomeUnknown is an interrupted call that is not declared repeatable.
	RefusalOutcomeUnknown Refusal = "outcome-unknown"
)

// Error makes a refusal an error value, so a store's error carries it for errors.Is.
func (r Refusal) Error() string { return "resume refused: " + string(r) }

// Refused marks a refusal for a caller that tests for one without importing this package.
func (Refusal) Refused() {}

// Class is whether a native module's recorded results can answer a replayed call.
type Class string

const (
	// classAnswer is a module whose results are data and can be answered from a record.
	classAnswer Class = "answer"
	// classLive is a module whose results name state in this process: a handle table,
	// an open file, a socket, a listener.
	classLive Class = "live"
)

// Header is the first line of a replay log. It names what ran, so a
// rerun can tell whether the record is its own.
type Header struct {
	Format string   `json:"format"`
	Script string   `json:"script"`
	Args   []string `json:"args"`
	// Imports maps each imported file's absolute path to the sha256 hex of its content.
	// It comes from the record's imports line, written once the entry compiles. It is
	// nil for a record that holds none.
	Imports map[string]string `json:"-"`
	// ImportsUnread is why a run that ended wrote no imports, empty when it wrote them.
	ImportsUnread string `json:"-"`
}

// importsLine is the line that holds a run's import digests. An empty map is a run
// that imported no file, so the key is kept.
type importsLine struct {
	Imports map[string]string `json:"imports"`
}

// unreadLine is the line a run that ended writes when its imports never arrived.
type unreadLine struct {
	Unread string `json:"imports_unread"`
}

// Call is one call in a record. A record holds one line when the call begins
// and one when it ends, both whole entries keyed by Seq; the last line wins.
type Call struct {
	Seq int `json:"seq"`
	// Parent is the Seq of the call this one ran inside, -1 for none.
	Parent     int     `json:"parent"`
	Name       string  `json:"name"`
	ArgsDigest string  `json:"args_digest"`
	End        callEnd `json:"end"`
	// Result is the call's value as JSON in the replay codec's form. It is empty
	// while the call is open, and when it raised or was unencodable.
	Result string `json:"result,omitempty"`
	// Error is the text of the error a raised call ended with.
	Error string `json:"error,omitempty"`
	// Unencodable names what the codec could not hold, empty when it held the result.
	Unencodable string `json:"unencodable,omitempty"`
	// StartedAt is unix milliseconds.
	StartedAt int64 `json:"started_at"`
}

// callLine is a Call as a line holds it. Result is embedded JSON, not an
// escaped string, so a reader of the file sees the value.
type callLine struct {
	Seq         *int             `json:"seq"`
	Parent      *int             `json:"parent"`
	Name        string           `json:"name"`
	ArgsDigest  string           `json:"args_digest"`
	End         callEnd          `json:"end"`
	Result      codec.RawMessage `json:"result,omitempty"`
	Error       string           `json:"error,omitempty"`
	Unencodable string           `json:"unencodable,omitempty"`
	StartedAt   int64            `json:"started_at"`
}

// MarshalJSON writes c as one record line. A Result that is not JSON is an error.
func (c Call) MarshalJSON() ([]byte, error) {
	if err := c.check(); err != nil {
		return nil, err
	}
	line := callLine{
		Seq: &c.Seq, Parent: &c.Parent, Name: c.Name, ArgsDigest: c.ArgsDigest, End: c.End,
		Error: c.Error, Unencodable: c.Unencodable, StartedAt: c.StartedAt,
	}
	if c.Result != "" {
		if !codec.Valid([]byte(c.Result)) {
			return nil, fmt.Errorf("call %d %s: result is not JSON", c.Seq, c.Name)
		}
		line.Result = codec.RawMessage(c.Result)
	}
	return codec.Marshal(line)
}

// UnmarshalJSON reads one record line. A line missing seq, parent or name is an error,
// and so is an end outside [callEnd]'s cases. Seq 0 and parent 0 are real calls, so
// a zero value would name one.
func (c *Call) UnmarshalJSON(data []byte) error {
	var line callLine
	if err := codec.Unmarshal(data, &line); err != nil {
		return err
	}
	if line.Seq == nil || line.Parent == nil {
		return errors.New("call line: seq and parent are required")
	}
	out := Call{
		Seq: *line.Seq, Parent: *line.Parent, Name: line.Name, ArgsDigest: line.ArgsDigest, End: line.End,
		Result: string(line.Result), Error: line.Error, Unencodable: line.Unencodable, StartedAt: line.StartedAt,
	}
	if err := out.check(); err != nil {
		return err
	}
	*c = out
	return nil
}

func (c Call) check() error {
	if c.Name == "" {
		return fmt.Errorf("call %d: name is required", c.Seq)
	}
	if c.Seq < 0 || c.Parent < -1 {
		return fmt.Errorf("call %d %s: seq must be 0 or more and parent -1 or more, got parent %d", c.Seq, c.Name, c.Parent)
	}
	if c.End == "" || !c.End.Valid() {
		return fmt.Errorf("call %d %s: end %q is not one of %v", c.Seq, c.Name, string(c.End), c.End.Values())
	}
	return nil
}
