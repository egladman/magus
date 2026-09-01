package run

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// runOnPTY is Exec's TTY branch: it runs c with its standard streams on a
// pseudo-terminal instead of pipes, forwarding everything the child draws to w
// (and into capture, when the caller asked for it).
//
// It cannot reuse cmd.Run(). Run() waits for the process AND for its own stream
// copying, but with a pty the copying is ours: the child holds the slave, we read
// the master, and the read only ends when the last writer closes the slave. So the
// order below is load-bearing.
//
//  1. Start the child. It now owns the slave.
//  2. Close OUR slave handle. If the parent keeps one open the master never sees
//     EOF, the copy blocks forever, and the whole run hangs - the classic pty
//     deadlock, and the reason this is not three lines.
//  3. Copy master -> writers until EIO, which is how a pty master reports "the
//     slave side is gone" and is the normal end of a session rather than a fault.
//     The copy runs on its own goroutine and is drained AFTER Wait, so nothing is
//     dropped between exit and the final read while still being bounded: see
//     drainPTY for why the read has no end of its own.
//
// onStarted is called once with the child's pid, from this goroutine, as soon as
// it exists. It is how the caller follows the process tree without reading
// c.Process across a goroutine boundary, which races with Start.
func runOnPTY(ctx context.Context, c *exec.Cmd, w io.Writer, capture *bytes.Buffer, opts ExecOptions, onStarted func(int)) error {
	if !ptySupported {
		_, _, err := openPTY(0, 0) // returns the platform's explanatory error
		return err
	}
	cols, rows := ptySize()
	if opts.TTYCols > 0 && opts.TTYRows > 0 {
		cols, rows = opts.TTYCols, opts.TTYRows
	}
	master, slave, err := openPTY(cols, rows)
	if err != nil {
		return err
	}
	// Registered before the close so it runs after it: closing the master is what
	// unblocks a stdin replay nobody read, and waiting first would deadlock on it.
	var stdinDone sync.WaitGroup
	defer stdinDone.Wait()
	defer master.Close()

	c.Stdout, c.Stderr = slave, slave
	// Stdin comes from the terminal too, so a child that prompts is talking to a
	// real tty. Exec already buffered any provided Stdin; feed it through the
	// master so the child reads it as typed input.
	stdinToWrite := ""
	if r, ok := c.Stdin.(*stringReaderMarker); ok {
		stdinToWrite = r.s
	}
	c.Stdin = slave
	c.SysProcAttr = ptySysProcAttr()
	// The child lays out for the geometry it is told; keep the env in step with the
	// winsize set on the master so a tool reading COLUMNS agrees with the terminal.
	c.Env = append(c.Env, "COLUMNS="+strconv.Itoa(cols), "LINES="+strconv.Itoa(rows))

	if err := c.Start(); err != nil {
		slave.Close()
		return err
	}
	if onStarted != nil {
		onStarted(c.Process.Pid)
	}
	// Step 2, and the whole reason this function exists.
	slave.Close()

	if stdinToWrite != "" {
		stdinDone.Add(1)
		go func() {
			defer stdinDone.Done()
			_, _ = io.WriteString(master, stdinToWrite)
		}()
	}

	dst := &detachableWriter{w: w}
	if capture != nil && opts.Capture {
		dst.w = io.MultiWriter(w, capture)
	}
	copyDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(dst, master)
		copyDone <- err
	}()

	waitErr := c.Wait()
	if err := drainPTY(ctx, c, copyDone, dst); err != nil && !isPTYClosed(err) {
		return err
	}
	return waitErr
}

// ptyDrainDelay bounds the master read once the child is gone, mirroring the
// WaitDelay exec.Cmd applies to pipes it owns. A test shortens it; nothing else
// writes it.
var ptyDrainDelay = 5 * time.Second

// drainPTY waits for the master copy to finish and bounds it.
//
// The copy has no end of its own: a pty master reports EOF only once the LAST
// writer closes the slave, and a grandchild that outlived the child still holds
// one - so an unbounded read hangs the build for as long as that grandchild
// lives. Killing the group is what actually ends it; the deadline before it lets
// a well-behaved tail of output finish first, and the deadline after it covers a
// read the kill did not free.
//
// Abandoning the copy detaches it, because Exec reads the capture buffer as soon
// as this returns.
func drainPTY(ctx context.Context, c *exec.Cmd, copyDone <-chan error, dst *detachableWriter) error {
	select {
	case err := <-copyDone:
		return err
	case <-ctx.Done():
	case <-time.After(ptyDrainDelay):
	}
	KillGroup(c)
	select {
	case err := <-copyDone:
		return err
	case <-time.After(ptyDrainDelay):
		dst.detach()
		return nil
	}
}

// detachableWriter forwards to w until detach, after which writes are dropped.
// It exists so a copy goroutine that outlives runOnPTY cannot write into the
// caller's buffers while Exec is reading them.
type detachableWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (d *detachableWriter) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.w == nil {
		return len(p), nil
	}
	return d.w.Write(p)
}

func (d *detachableWriter) detach() {
	d.mu.Lock()
	d.w = nil
	d.mu.Unlock()
}

// isPTYClosed reports whether err is the read error a pty master returns once the
// slave has been closed. Unix answers EIO here, which every other context would
// call a failure; on a pty it is end-of-stream.
func isPTYClosed(err error) bool {
	if errors.Is(err, io.EOF) {
		return true
	}
	var perr *os.PathError
	if errors.As(err, &perr) {
		return perr.Err.Error() == "input/output error"
	}
	return err != nil && err.Error() == "input/output error"
}

// stringReaderMarker lets the TTY branch recover Stdin text that Exec already
// wrapped in a reader, so it can be replayed through the pty master instead. Exec
// builds it; nothing else should.
type stringReaderMarker struct {
	io.Reader
	s string
}
