package report

import (
	"fmt"
	"log/slog"
	"strconv"

	"github.com/egladman/magus/internal/json"
)

// envelope is the wire-format wrapper; drain goroutine marshals body and splices
// "schema" and "type" fields so every line starts with `{"schema":5,"type":"...",`.
type envelope struct {
	Type string
	Body any
}

// appendJSONL appends the envelope to dst as one JSONL line. A body that does not encode
// to a JSON object is replaced by a run.notice naming the failure, so a reader still gets
// a record where the event would have been and never a gap.
func (e envelope) appendJSONL(dst []byte) []byte {
	body, err := e.body()
	if err != nil {
		fallback := Notice{Level: slog.LevelError, Message: err.Error()}
		if n, ok := e.Body.(Notice); ok {
			fallback = Notice{Level: n.Level, Code: n.Code, Message: n.Message, Attrs: map[string]any{"encode_error": err.Error()}}
		}
		e = envelope{Type: TypeNotice, Body: fallback}
		// A Notice of strings always encodes.
		body, _ = e.body()
	}
	dst = append(dst, `{"schema":`...)
	dst = strconv.AppendInt(dst, Schema, 10)
	dst = append(dst, `,"type":"`...)
	dst = append(dst, e.Type...)
	dst = append(dst, '"')
	if len(body) > 2 {
		dst = append(dst, ',')
		dst = append(dst, body[1:len(body)-1]...)
	}
	return append(dst, '}', '\n')
}

// body is e.Body marshaled, refusing anything but a JSON object.
func (e envelope) body() ([]byte, error) {
	body, err := json.Marshal(e.Body)
	if err != nil {
		return nil, fmt.Errorf("report: cannot encode %s event: %w", e.Type, err)
	}
	if len(body) < 2 || body[0] != '{' || body[len(body)-1] != '}' {
		return nil, fmt.Errorf("report: %s event encodes to %q, not a JSON object", e.Type, body)
	}
	return body, nil
}
