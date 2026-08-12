package proc

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/egladman/magus/internal/json"
)

// maxFrameBytes is the maximum size of a single JSONL frame. Requests from
// child processes are untrusted in principle; this cap prevents an oversized
// line from exhausting server memory before the args-count check fires.
const maxFrameBytes = 4 << 20 // 4 MiB

// writeFrame serialises body via json.Marshal and writes a single JSONL line
// to w in the form {"type":"<typeName>",<body fields>}\n. body must marshal
// to a JSON object; any other shape (array, scalar) is a programming error
// and returns an error.
//
// This mirrors the splice pattern in internal/report/envelope.go.
func writeFrame(w io.Writer, typeName string, body any) error {
	raw, err := json.Marshal(body)
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
	if e := json.Unmarshal(line, &head); e != nil {
		return "", nil, fmt.Errorf("proc: decode frame type: %w", e)
	}
	if head.Type == "" {
		return "", nil, fmt.Errorf("proc: frame missing \"type\" field")
	}
	return head.Type, line, nil
}

// readFrameCtx reads one frame from conn like readFrame, but returns as soon as ctx
// is cancelled even though the read underneath has no deadline of its own and
// readFrame takes a plain io.Reader with no way to observe ctx.
//
// It deliberately imposes no wall-clock timeout beyond what ctx itself carries: an
// adopted run can hold this connection open for as long as the daemon-side build
// takes (documented elsewhere as "many minutes"), so a fixed deadline here would
// kill a healthy long-running call. Only ctx cancellation - the caller's own
// context.WithTimeout, or a parent giving up - ends the read early; a wedged daemon
// that never replies is exactly the case this unblocks (see readFrame's callers in
// client.go, none of which previously had any way to observe ctx once past Dial).
func readFrameCtx(ctx context.Context, conn net.Conn) (typeName string, line []byte, err error) {
	type result struct {
		typeName string
		line     []byte
		err      error
	}
	done := make(chan result, 1)
	go func() {
		typeName, line, err := readFrame(conn)
		done <- result{typeName, line, err}
	}()

	select {
	case r := <-done:
		return r.typeName, r.line, r.err
	case <-ctx.Done():
		// Force the blocked Read to return immediately. The goroutine above still
		// sends on done (buffered, capacity 1) once it unblocks, so it can never
		// leak even though nothing receives from it after we return here.
		_ = conn.SetReadDeadline(time.Now())
		return "", nil, ctx.Err()
	}
}
