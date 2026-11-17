package render

import (
	"fmt"
	"io"
	"os"
)

// Tee is where an invocation mirrors its structured output: the --tee file.
type Tee struct {
	// Path is the --tee value, empty when none was given.
	Path string
	// opened is the capture already replaced during this invocation.
	opened string
}

// Reset starts the next invocation's capture fresh.
func (t *Tee) Reset() { t.opened = "" }

// StructuredStdout is where structured output goes besides the --tee file. --silent
// with --tee means the caller captures the record in the file and wants stdout empty,
// so it does not pay for the same record twice in its context.
func (t *Tee) StructuredStdout(out io.Writer, silent bool) io.Writer {
	if silent && t.Path != "" {
		return io.Discard
	}
	return out
}

// Destination returns the writer for structured output over out, mirrored to the
// --tee file when set; the caller runs the returned close once it is done writing.
func (t *Tee) Destination(out io.Writer, silent bool) (io.Writer, func() error, error) {
	if t.Path == "" {
		return out, func() error { return nil }, nil
	}
	f, err := t.openFile(t.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("--tee: %w", err)
	}
	return io.MultiWriter(t.StructuredStdout(out, silent), f), f.Close, nil
}

// A command may open several destinations while streaming records. Truncate only
// the first, so a fresh invocation replaces the old capture without losing its own records.
func (t *Tee) openFile(path string) (*os.File, error) {
	flags := os.O_WRONLY | os.O_APPEND | os.O_CREATE
	if t.opened != path {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if err == nil {
		t.opened = path
	}
	return f, err
}

// LazyFile opens the tee file on the first write, so a format that keeps stdout
// empty leaves no --tee file behind. Not safe for concurrent writes.
type LazyFile struct {
	tee *Tee
	f   *os.File
	err error
}

// NewLazyFile returns a writer over t's file, opened on its first write.
func NewLazyFile(t *Tee) *LazyFile { return &LazyFile{tee: t} }

func (l *LazyFile) Write(p []byte) (int, error) {
	if l.f == nil && l.err == nil {
		l.f, l.err = l.tee.openFile(l.tee.Path)
	}
	if l.err != nil {
		return 0, fmt.Errorf("--tee: %w", l.err)
	}
	return l.f.Write(p)
}

// Close closes the file if a write opened it.
func (l *LazyFile) Close() error {
	if l.f == nil {
		return nil
	}
	return l.f.Close()
}
