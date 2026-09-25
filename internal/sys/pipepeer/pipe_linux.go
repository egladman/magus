//go:build linux

package pipepeer

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ReadEnd returns the pipe open at fd in pid. Both ends of a linux pipe share one pipefs
// inode, which /proc renders as the link target "pipe:[<inode>]".
func ReadEnd(pid, fd int) (Pipe, error) {
	ino, ok := pipeInode(fdPath(pid, fd))
	if !ok {
		return Pipe{}, ErrNotPipe
	}
	return Pipe{id: ino}, nil
}

// WriteEnd returns the pipe open at fd in pid. Both ends share the inode, so it names
// the same Pipe ReadEnd does.
func WriteEnd(pid, fd int) (Pipe, error) { return ReadEnd(pid, fd) }

// Writers returns every process this user can inspect that holds p's write end open.
// Processes that hold only the read end are not writers.
func (p Pipe) Writers() ([]int, error) { return p.holders(writable) }

// WrittenBy reports whether pid holds p's write end open right now. A process that has
// exited, or that this user may not inspect, is not a writer.
func (p Pipe) WrittenBy(pid int) bool { return p.heldBy(pid, writable) }

// Readers returns every process this user can inspect that holds p's read end open.
func (p Pipe) Readers() ([]int, error) { return p.holders(readable) }

// ReadBy reports whether pid holds p's read end open right now.
func (p Pipe) ReadBy(pid int) bool { return p.heldBy(pid, readable) }

func (p Pipe) holders(end func(pid int, fd string) bool) ([]int, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, fmt.Errorf("pipepeer: read /proc: %w", err)
	}
	var out []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if p.heldBy(pid, end) {
			out = append(out, pid)
		}
	}
	return out, nil
}

func (p Pipe) heldBy(pid int, end func(pid int, fd string) bool) bool {
	dir := procPath(pid, "fd")
	fds, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, fd := range fds {
		ino, ok := pipeInode(filepath.Join(dir, fd.Name()))
		if !ok || ino != p.id {
			continue
		}
		if end(pid, fd.Name()) {
			return true
		}
	}
	return false
}

// Args returns pid's argument vector, argv[0] first.
func Args(pid int) ([]string, error) {
	raw, err := os.ReadFile(procPath(pid, "cmdline"))
	if err != nil {
		return nil, fmt.Errorf("pipepeer: read cmdline of %d: %w", pid, err)
	}
	raw = bytes.TrimSuffix(raw, []byte{0})
	if len(raw) == 0 {
		return nil, nil
	}
	return strings.Split(string(raw), "\x00"), nil
}

// Parent returns pid's parent process id.
func Parent(pid int) (int, error) {
	raw, err := os.ReadFile(procPath(pid, "stat"))
	if err != nil {
		return 0, fmt.Errorf("pipepeer: read stat of %d: %w", pid, err)
	}
	// The command name is parenthesized and may itself contain spaces or parentheses,
	// so the fields are counted from the LAST closing parenthesis: state, then ppid.
	i := bytes.LastIndexByte(raw, ')')
	if i < 0 {
		return 0, fmt.Errorf("pipepeer: malformed stat for %d", pid)
	}
	fields := strings.Fields(string(raw[i+1:]))
	if len(fields) < 2 {
		return 0, fmt.Errorf("pipepeer: malformed stat for %d", pid)
	}
	return strconv.Atoi(fields[1])
}

// executable stats the file pid is running. /proc/<pid>/exe resolves to the inode even
// after the file is replaced or unlinked, which a path comparison would not survive.
func executable(pid int) (os.FileInfo, error) {
	return os.Stat(procPath(pid, "exe"))
}

// procPath is /proc/<pid>/<elem...>.
func procPath(pid int, elem ...string) string {
	return procRoot + "/" + strconv.Itoa(pid) + "/" + strings.Join(elem, "/")
}

const procRoot = "/proc"

func fdPath(pid, fd int) string {
	return procPath(pid, "fd", strconv.Itoa(fd))
}

func pipeInode(link string) (uint64, bool) {
	target, err := os.Readlink(link)
	if err != nil {
		return 0, false
	}
	rest, ok := strings.CutPrefix(target, "pipe:[")
	if !ok {
		return 0, false
	}
	ino, err := strconv.ParseUint(strings.TrimSuffix(rest, "]"), 10, 64)
	if err != nil {
		return 0, false
	}
	return ino, true
}

// writable reads the descriptor's open flags from fdinfo. A FIFO opened O_RDWR is a
// writer too, since it can write.
func writable(pid int, fd string) bool {
	mode, ok := accessMode(pid, fd)
	return ok && (mode == syscall.O_WRONLY || mode == syscall.O_RDWR)
}

// readable is writable's counterpart; a FIFO opened O_RDWR reads too.
func readable(pid int, fd string) bool {
	mode, ok := accessMode(pid, fd)
	return ok && (mode == syscall.O_RDONLY || mode == syscall.O_RDWR)
}

func accessMode(pid int, fd string) (uint64, bool) {
	f, err := os.Open(procPath(pid, "fdinfo", fd))
	if err != nil {
		return 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		v, ok := strings.CutPrefix(sc.Text(), "flags:")
		if !ok {
			continue
		}
		flags, err := strconv.ParseUint(strings.TrimSpace(v), 8, 64)
		if err != nil {
			return 0, false
		}
		return flags & syscall.O_ACCMODE, true
	}
	return 0, false
}
