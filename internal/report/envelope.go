package report

import (
	"bufio"
	"fmt"
	"strconv"

	"github.com/egladman/magus/internal/codec"
)

// envelope is the wire-format wrapper; drain goroutine marshals body and splices
// "schema" and "type" fields so every line starts with `{"schema":2,"type":"...",`.
type envelope struct {
	Type string
	Body any
}

// writeJSONL writes the envelope as one JSONL line; errors only on marshal failure or non-object body.
func (e envelope) writeJSONL(bw *bufio.Writer) error {
	body, err := codec.Marshal(e.Body)
	if err != nil {
		return fmt.Errorf("report: marshal %T: %w", e.Body, err)
	}
	if len(body) < 2 || body[0] != '{' || body[len(body)-1] != '}' {
		return fmt.Errorf("report: %T marshals to %q (want a JSON object)", e.Body, body)
	}
	if _, err := bw.WriteString(`{"schema":`); err != nil {
		return err
	}
	if _, err := bw.WriteString(strconv.Itoa(Schema)); err != nil {
		return err
	}
	if _, err := bw.WriteString(`,"type":"`); err != nil {
		return err
	}
	if _, err := bw.WriteString(e.Type); err != nil {
		return err
	}
	if err := bw.WriteByte('"'); err != nil {
		return err
	}
	if len(body) > 2 {
		if err := bw.WriteByte(','); err != nil { // skip leading `{` of body
			return err
		}
		if _, err := bw.Write(body[1 : len(body)-1]); err != nil {
			return err
		}
	}
	if err := bw.WriteByte('}'); err != nil {
		return err
	}
	return bw.WriteByte('\n')
}
