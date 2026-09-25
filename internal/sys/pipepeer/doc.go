// Package pipepeer proves, from the kernel, which processes sit at the other end of a
// pipe: who holds its write end, what they are executing, and with what arguments.
//
// It exists so a magus reading another magus's output can tell a pipeline predecessor
// from an unrelated process. Every answer is read live from the kernel (/proc on linux,
// proc_info on darwin) rather than from anything a process says about itself, so a
// claim cannot be forged by writing a file.
//
// Only linux and darwin can answer. Everywhere else every function returns
// ErrUnsupported, and callers treat that as "no proven peer".
package pipepeer

import (
	"errors"
	"os"
	"slices"
)

// ErrNotPipe is returned for a descriptor that is not a pipe: a terminal, a regular
// file, /dev/null, a socket.
var ErrNotPipe = errors.New("pipepeer: not a pipe")

// ErrUnsupported is returned on a platform whose kernel exposes no way to prove a pipe's
// peers.
var ErrUnsupported = errors.New("pipepeer: pipe peers cannot be proven on this platform")

// A Pipe identifies one pipe, as seen from a process holding its read end.
type Pipe struct {
	// id is the pipe inode on linux, and the write end's kernel handle on darwin, whose
	// two ends carry distinct inodes.
	id uint64
}

// SameExecutable reports whether pid is running the same executable file as this
// process. A copy of the binary at another path is a different file and does not match;
// a hard link does.
func SameExecutable(pid int) bool {
	theirs, err := executable(pid)
	if err != nil {
		return false
	}
	ours, err := executable(os.Getpid())
	if err != nil {
		return false
	}
	return os.SameFile(theirs, ours)
}

// ExecPending reports whether pid runs the same executable file as its parent with the
// same arguments, which is what a child looks like between fork and exec: a shell's
// pipeline stage, or any other process's child that briefly holds a copy of every
// descriptor its parent had. What such a process runs proves nothing yet; a caller
// asking what a pipe's peer runs skips it and asks again.
func ExecPending(pid int) bool {
	parent, err := Parent(pid)
	if err != nil || parent <= 0 {
		return false
	}
	theirs, err := executable(pid)
	if err != nil {
		return false
	}
	parents, err := executable(parent)
	if err != nil || !os.SameFile(theirs, parents) {
		return false
	}
	// Arguments that cannot be read prove no exec either, so they count as pending.
	args, err := Args(pid)
	if err != nil {
		return true
	}
	parentArgs, err := Args(parent)
	return err != nil || slices.Equal(args, parentArgs)
}
