package broker

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/egladman/magus/internal/json"
)

// frameReader reads frames off one connection. It keeps its buffer across calls: a
// connection carries many frames, and a reader built per frame would drop whatever the
// last one read past its newline.
type frameReader struct{ br *bufio.Reader }

func newFrameReader(r io.Reader) *frameReader {
	return &frameReader{br: bufio.NewReaderSize(r, 16<<10)}
}

// read returns the next frame, or io.EOF when the peer closed between frames.
func (r *frameReader) read() (frame, error) {
	var line []byte
	for {
		chunk, err := r.br.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > maxFrameBytes {
			return frame{}, fmt.Errorf("broker: frame exceeds %d bytes", maxFrameBytes)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			if len(bytes.TrimSpace(line)) == 0 {
				return frame{}, err
			}
			return frame{}, fmt.Errorf("broker: truncated frame: %w", err)
		}
		break
	}
	var f frame
	if err := json.Unmarshal(bytes.TrimSpace(line), &f); err != nil {
		return frame{}, fmt.Errorf("broker: decode frame: %w", err)
	}
	if f.Type == "" {
		return frame{}, errors.New("broker: frame has no type")
	}
	return f, nil
}

// frameWriter writes whole frames; safe for concurrent use, since several requests on
// one connection may be answered at once.
type frameWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (w *frameWriter) write(typ string, id uint64, body any) error {
	f := frame{Type: typ, ID: id}
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("broker: encode %s: %w", typ, err)
		}
		f.Body = raw
	}
	line, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("broker: encode %s: %w", typ, err)
	}
	line = append(line, '\n')
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err = w.w.Write(line)
	return err
}

// decodeBody reads f's body into v. An empty body leaves v at its zero value.
func decodeBody(f frame, v any) error {
	if len(f.Body) == 0 {
		return nil
	}
	return json.Unmarshal(f.Body, v)
}
