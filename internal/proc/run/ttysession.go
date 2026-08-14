//go:build darwin || linux

package run

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
)

// TTYSession runs a command on a pseudo-terminal and lets the caller INTERLEAVE
// input and output: type something, watch what it draws, type the next thing.
//
// [Exec] with TTY set cannot do this. It buffers all of stdin up front and hands
// back the transcript at the end, which is right for capturing a tool's output
// and useless for recording an interactive one - a picker fed its keystrokes in
// a single write processes them faster than it draws, so the capture holds the
// final state and none of the moments worth showing.
//
// It exists for the documentation recorder. Nothing in a build path should be
// typing at a subprocess.
type TTYSession struct {
	cmd    *exec.Cmd
	master *os.File
	done   chan struct{}

	mu  sync.Mutex
	buf bytes.Buffer
}

// StartTTY launches name on a pty of the given size and begins draining it.
//
// The drain runs in its own goroutine and never stops early: a child that is
// still drawing when the caller stops reading would block on a full pty buffer,
// which looks exactly like a hang in the recording.
func StartTTY(ctx context.Context, name string, args []string, dir string, cols, rows int) (*TTYSession, error) {
	if !ptySupported {
		_, _, err := openPTY(0, 0)
		return nil, err
	}
	master, slave, err := openPTY(cols, rows)
	if err != nil {
		return nil, err
	}

	c := exec.CommandContext(ctx, name, args...)
	c.Dir = dir
	c.Stdin, c.Stdout, c.Stderr = slave, slave, slave
	c.SysProcAttr = ptySysProcAttr()
	// The child lays out for the geometry it is told, so COLUMNS and LINES have
	// to agree with the winsize or a tool reading them draws to a width the
	// terminal does not have.
	c.Env = append(c.Environ(), "COLUMNS="+strconv.Itoa(cols), "LINES="+strconv.Itoa(rows))
	if err := c.Start(); err != nil {
		slave.Close()
		master.Close()
		return nil, err
	}
	// The parent's copy of the slave is closed so the drain sees EOF when the
	// child exits; the child keeps its own descriptors.
	slave.Close()

	s := &TTYSession{cmd: c, master: master, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		b := make([]byte, 4096)
		for {
			n, err := master.Read(b)
			if n > 0 {
				s.mu.Lock()
				s.buf.Write(b[:n])
				s.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return s, nil
}

// Type sends keystrokes as though a person had typed them.
func (s *TTYSession) Type(keys string) error {
	_, err := io.WriteString(s.master, keys)
	return err
}

// Output is everything the child has drawn so far.
func (s *TTYSession) Output() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.Clone(s.buf.Bytes())
}

// Close waits for the child to exit and for the drain to finish, so Output is
// complete when it returns.
func (s *TTYSession) Close() error {
	err := s.cmd.Wait()
	_ = s.master.Close()
	<-s.done
	if err != nil {
		return fmt.Errorf("session %s: %w", s.cmd.Path, err)
	}
	return nil
}
