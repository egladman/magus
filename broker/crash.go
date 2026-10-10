package broker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

// errNoFilePassing is a platform, or a connection, that cannot pass files.
var errNoFilePassing = errors.New("broker: this connection cannot pass file descriptors")

// maxPassedFiles is the most descriptors one frame carries; a crash watch passes two.
const maxPassedFiles = 2

// maxCrashReport bounds what the broker keeps of one crash report. The rest is read and
// dropped rather than left unread: the runtime writes the report blocking, so a reader
// that stopped would hang the dying process.
const maxCrashReport = 8 << 20

// WithCrashHint makes the client ask each broker it connects to to watch this process
// for a crash: the runtime copies an unrecovered panic or fatal error on any goroutine
// to a pipe the broker reads (runtime/debug.SetCrashOutput), and the broker then writes
// hint to this process's stderr, after Go's trace, and saves the report.
//
// SetCrashOutput is process-wide, so only the client a process holds for its life should
// set this; a library embedding the broker client must not. A broker that cannot take
// the watch, or a platform that cannot pass file descriptors, leaves the process
// crashing with Go's trace alone.
func WithCrashHint(hint string) ClientOption {
	return func(c *Client) { c.crashHint = hint }
}

// WithCrashDir saves each crash report a watched client sends under dir. Without it the
// broker writes the hint and keeps no report.
func WithCrashDir(dir string) Option { return func(o *serveOptions) { o.crashDir = dir } }

// watchCrashes registers this process with the broker on cn. It runs on every new
// connection: SetCrashOutput replaces and closes the previous pipe, so a broker watching
// an older one reads its end and lets it go.
func (c *Client) watchCrashes(ctx context.Context, cn *conn) {
	r, w, err := os.Pipe()
	if err != nil {
		return
	}
	defer func() { _ = w.Close() }()
	err = cn.roundTripFiles(ctx, c.next(), typeCrashWatch, crashWatchRequest{Hint: c.crashHint},
		[]*os.File{r, os.Stderr}, typeCrashReply, nil, nil)
	// This process keeps no read end, so with the broker gone a crash write fails rather
	// than blocking on a pipe nobody drains.
	_ = r.Close()
	if err != nil {
		return
	}
	_ = debug.SetCrashOutput(w, debug.CrashOptions{})
}

// watchCrash reads report until every writer is gone: the watched process exited, or its
// runtime moved crash output to a newer pipe. Anything read is a crash; the broker
// saves it and writes hint to the process's stderr, then lets both files go.
func (s *server) watchCrash(h hello, hint string, report, stderr *os.File) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		_ = report.Close()
		_ = stderr.Close()
		return
	}
	s.watches[report] = struct{}{}
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		defer func() { _ = stderr.Close() }()
		var body bytes.Buffer
		_, _ = io.CopyN(&body, report, maxCrashReport)
		_, _ = io.Copy(io.Discard, report)
		s.mu.Lock()
		delete(s.watches, report)
		closing := s.closing
		s.mu.Unlock()
		_ = report.Close()
		if body.Len() == 0 || closing {
			return
		}
		msg := hint
		if path, err := s.saveCrash(h, body.Bytes()); err != nil {
			s.opts.log.WarnContext(s.ctx, "could not save a crash report", slog.Int("pid", h.PID), slog.String("error", err.Error()))
		} else if path != "" {
			msg += "The crash report is saved at " + path + ".\n"
		}
		_, _ = io.WriteString(stderr, msg)
		s.opts.log.WarnContext(s.ctx, "a watched process crashed", slog.Int("pid", h.PID), slog.String("command", strings.Join(h.Argv, " ")))
	}()
}

// saveCrash writes a report under the crash dir, headed by who crashed, and returns its
// path; "" when no dir is set.
func (s *server) saveCrash(h hello, report []byte) (string, error) {
	if s.opts.crashDir == "" {
		return "", nil
	}
	if err := os.MkdirAll(s.opts.crashDir, 0o700); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	path := filepath.Join(s.opts.crashDir, fmt.Sprintf("%s-%d.txt", now.Format("20060102T150405Z"), h.PID))
	var b bytes.Buffer
	fmt.Fprintf(&b, "magus %s (pid %d) crashed at %s\n", h.Version, h.PID, now.Format(time.RFC3339))
	fmt.Fprintf(&b, "command: %s\n", strings.Join(h.Argv, " "))
	fmt.Fprintf(&b, "dir: %s\n\n", h.Dir)
	b.Write(report)
	return path, os.WriteFile(path, b.Bytes(), 0o600)
}

// closeWatches lets go of every crash report still being read, so Serve can return
// while watched processes live on; one that crashes later fails its write instead.
func (s *server) closeWatches() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for f := range s.watches {
		_ = f.Close()
	}
}
