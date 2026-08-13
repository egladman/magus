// Command magus-asciicast records a terminal session to an asciicast v2 file:
// everything a command printed, including the escape sequences that colour it and
// redraw its status line, with a timestamp on each chunk so a player can replay it
// at the speed it actually happened.
//
// It sits beside the VHS tapes in tapes/ and answers a different question. VHS
// renders a GIF: pixels, about a megabyte, no selectable text, and the escape
// sequences are gone by the time anyone looks at it. This records the escape
// sequences THEMSELVES, so a player replays the session the way the terminal drew
// it - the live status region, the progress counters, the cursor moves - as real
// text, in a few kilobytes.
//
// The pseudo-terminal is the whole trick. magus checks isatty to decide whether to
// colourise and whether to draw its live regions at all, so a session captured
// through a pipe would be the WRONG output: the plain non-TTY fallback, missing
// exactly the behaviour worth showing. Running the child under a pty makes isatty
// true and records what a person would actually see.
//
// Written in Go rather than as a script for the reason the rest of this repository
// is: adding a second toolchain to record a demo of a tool whose pitch is "no
// second toolchain" would be an odd way to make the point. x/sys/unix is already a
// dependency, and the pty dance is three ioctls.
//
// Usage:
//
//	magus-asciicast -out FILE [-cols N] [-rows N] -- CMD [ARGS...]
//
// Output is asciicast v2 (https://docs.asciinema.org/manual/asciicast/v2/): one
// JSON header line, then one [elapsed, "o", data] line per read.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/egladman/magus/internal/json"
)

// header is the asciicast v2 first line. Env is pinned rather than inherited so a
// recording does not depend on whichever terminal produced it.
type header struct {
	Version int               `json:"version"`
	Width   int               `json:"width"`
	Height  int               `json:"height"`
	Env     map[string]string `json:"env"`
}

func main() {
	out := flag.String("out", "", "asciicast v2 file to write (required)")
	cols := flag.Int("cols", 96, "terminal columns")
	rows := flag.Int("rows", 28, "terminal rows")
	flag.Parse()
	if *out == "" || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: magus-asciicast -out FILE [-cols N] [-rows N] -- CMD [ARGS...]")
		os.Exit(2)
	}
	if err := run(*out, *cols, *rows, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "magus-asciicast:", err)
		os.Exit(1)
	}
}

func run(out string, cols, rows int, argv []string) error {
	ptmx, tty, err := openPTY(cols, rows)
	if err != nil {
		return err
	}
	defer ptmx.Close()

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	// A new session with the tty as controlling terminal, or job control in the
	// recorded shell misbehaves and the child never sees a real terminal.
	cmd.SysProcAttr = sysProcAttr()
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		fmt.Sprintf("COLUMNS=%d", cols),
		fmt.Sprintf("LINES=%d", rows),
	)
	if err := cmd.Start(); err != nil {
		tty.Close()
		return err
	}
	// The parent must not hold the slave open, or the read below never sees EOF
	// when the child exits and the recording hangs forever.
	tty.Close()

	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()

	hdr, err := json.Marshal(header{
		Version: 2, Width: cols, Height: rows,
		Env: map[string]string{"TERM": "xterm-256color", "SHELL": "/bin/bash"},
	})
	if err != nil {
		return err
	}
	if _, err := f.Write(append(hdr, '\n')); err != nil {
		return err
	}

	start := time.Now()
	buf := make([]byte, 64*1024)
	events := 0
	for {
		n, readErr := ptmx.Read(buf)
		if n > 0 {
			// [elapsed, "o", data]. json.Marshal on a []any keeps the array shape
			// the format requires; the string is UTF-8 with the escapes intact.
			line, err := json.Marshal([]any{
				float64(time.Since(start).Microseconds()) / 1e6,
				"o",
				string(buf[:n]),
			})
			if err != nil {
				return err
			}
			if _, err := f.Write(append(line, '\n')); err != nil {
				return err
			}
			events++
		}
		if readErr != nil {
			// EIO is how a pty master reports "the slave side closed", which is the
			// normal end of a recording rather than a failure.
			break
		}
	}
	_ = cmd.Wait()

	info, _ := f.Stat()
	fmt.Fprintf(os.Stderr, "magus-asciicast: wrote %s (%d events, %d bytes) <- %s\n",
		out, events, info.Size(), strings.Join(argv, " "))
	return nil
}

// openPTY allocates a master/slave pair and sizes it. Three steps, in this order:
// open the multiplexer, hand the slave to the child's session, and set the window
// size so the program lays out for the geometry the header claims.
func openPTY(cols, rows int) (*os.File, *os.File, error) {
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}
	// grantpt/unlockpt equivalents are platform-specific and live beside ptsname.
	name, err := ptsname(ptmx)
	if err != nil {
		ptmx.Close()
		return nil, nil, err
	}
	tty, err := os.OpenFile(name, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		ptmx.Close()
		return nil, nil, fmt.Errorf("open %s: %w", name, err)
	}
	ws := &unix.Winsize{Row: uint16(rows), Col: uint16(cols)}
	if err := unix.IoctlSetWinsize(int(ptmx.Fd()), unix.TIOCSWINSZ, ws); err != nil {
		tty.Close()
		ptmx.Close()
		return nil, nil, fmt.Errorf("set winsize: %w", err)
	}
	return ptmx, tty, nil
}
