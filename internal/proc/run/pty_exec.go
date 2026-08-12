package run

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
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
//  4. Only then Wait, so no output is dropped between exit and the final read.
func runOnPTY(c *exec.Cmd, w io.Writer, capture *bytes.Buffer, opts ExecOptions) error {
	if !ptySupported {
		_, _, err := openPTY(0, 0) // returns the platform's explanatory error
		return err
	}
	cols, rows := ptySize()
	master, slave, err := openPTY(cols, rows)
	if err != nil {
		return err
	}
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
	// Step 2, and the whole reason this function exists.
	slave.Close()

	if stdinToWrite != "" {
		go func() {
			_, _ = io.WriteString(master, stdinToWrite)
		}()
	}

	dst := w
	if capture != nil && opts.Capture {
		dst = io.MultiWriter(w, capture)
	}
	if _, err := io.Copy(dst, master); err != nil && !isPTYClosed(err) {
		_ = c.Wait()
		return err
	}
	return c.Wait()
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
