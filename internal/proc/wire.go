package proc

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/egladman/magus/internal/codec"
)

// maxFrameBytes is the maximum size of a single JSONL frame. Requests from
// child processes are untrusted in principle; this cap prevents an oversized
// line from exhausting server memory before the args-count check fires.
const maxFrameBytes = 4 << 20 // 4 MiB

// writeFrame serialises body via codec.Marshal and writes a single JSONL line
// to w in the form {"type":"<typeName>",<body fields>}\n. body must marshal
// to a JSON object; any other shape (array, scalar) is a programming error
// and returns an error.
//
// This mirrors the splice pattern in internal/report/envelope.go.
func writeFrame(w io.Writer, typeName string, body any) error {
	raw, err := codec.Marshal(body)
	if err != nil {
		return fmt.Errorf("proc: marshal %T: %w", body, err)
	}
	if len(raw) < 2 || raw[0] != '{' || raw[len(raw)-1] != '}' {
		return fmt.Errorf("proc: %T marshals to %q (want JSON object)", body, raw)
	}

	// Build: {"type":"<typeName>",<body fields without leading {>}\n
	var buf bytes.Buffer
	buf.Grow(len(typeName) + len(raw) + 16)
	buf.WriteString(`{"type":"`)
	buf.WriteString(typeName)
	buf.WriteByte('"')
	if len(raw) > 2 {
		// body is `{...fields...}`; drop the leading `{` and prepend `,`.
		buf.WriteByte(',')
		buf.Write(raw[1 : len(raw)-1])
	}
	buf.WriteByte('}')
	buf.WriteByte('\n')

	_, err = w.Write(buf.Bytes())
	return err
}

// readFrame reads one JSONL line from r, extracts the "type" field, and returns
// the full line bytes. Lines longer than maxFrameBytes are rejected.
func readFrame(r io.Reader) (typeName string, line []byte, err error) {
	br := bufio.NewReaderSize(io.LimitReader(r, int64(maxFrameBytes)+1), 4<<10)
	line, err = br.ReadBytes('\n')
	line = bytes.TrimRight(line, "\n")
	if len(line) > maxFrameBytes {
		return "", nil, fmt.Errorf("proc: frame exceeds %d bytes", maxFrameBytes)
	}
	if len(line) == 0 {
		if err == nil {
			err = io.EOF
		}
		return "", nil, err
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return "", nil, err
	}

	var head struct {
		Type string `json:"type"`
	}
	if e := codec.Unmarshal(line, &head); e != nil {
		return "", nil, fmt.Errorf("proc: decode frame type: %w", e)
	}
	if head.Type == "" {
		return "", nil, fmt.Errorf("proc: frame missing \"type\" field")
	}
	return head.Type, line, nil
}
