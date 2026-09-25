//go:build darwin

package pipepeer

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The proc_info interface behind libproc (xnu bsd/sys/proc_info.h). x/sys wraps none of
// it, and calling the syscall directly keeps the binary free of cgo.
const (
	procInfoCallListPIDs  = 1
	procInfoCallPIDInfo   = 2
	procInfoCallPIDFDInfo = 3

	procUIDOnly       = 4
	procPIDListFDs    = 1
	procPIDPathInfo   = 11
	procPIDFDPipeInfo = 6
	proxFDTypePipe    = 6

	// struct pipe_fdinfo: a 24-byte proc_fileinfo, then pipe_info, whose 136-byte
	// vinfo_stat is followed by pipe_handle and pipe_peerhandle.
	pipeFDInfoSize    = 184
	pipeHandleOffset  = 24 + 136
	pipePeerOffset    = pipeHandleOffset + 8
	procFDInfoSize    = 8 // struct proc_fdinfo: int32 fd, uint32 type
	maxPathLen        = 1024
	procPIDPathBufLen = 4 * maxPathLen
)

// ReadEnd returns the pipe open at fd in pid. darwin gives each end of a pipe its own
// identity, so the pipe is named by the WRITE end's handle: the read end's peer handle.
func ReadEnd(pid, fd int) (Pipe, error) {
	_, peer, ok := pipeHandles(pid, fd)
	if !ok {
		return Pipe{}, ErrNotPipe
	}
	return Pipe{id: peer}, nil
}

// WriteEnd returns the pipe open at fd in pid, seen from its write end: the same Pipe
// ReadEnd names from the other end.
func WriteEnd(pid, fd int) (Pipe, error) {
	self, _, ok := pipeHandles(pid, fd)
	if !ok {
		return Pipe{}, ErrNotPipe
	}
	return Pipe{id: self}, nil
}

// Readers returns every process of this user that holds p's read end open.
func (p Pipe) Readers() ([]int, error) {
	pids, err := userPIDs()
	if err != nil {
		return nil, err
	}
	var out []int
	for _, pid := range pids {
		if p.ReadBy(pid) {
			out = append(out, pid)
		}
	}
	return out, nil
}

// ReadBy reports whether pid holds p's read end open right now. A read end's peer is
// the write end that names the pipe.
func (p Pipe) ReadBy(pid int) bool {
	for _, fd := range pipeFDs(pid) {
		if _, peer, ok := pipeHandles(pid, fd); ok && peer == p.id {
			return true
		}
	}
	return false
}

// Writers returns every process of this user that holds p's write end open. Processes
// that hold only the read end are not writers.
func (p Pipe) Writers() ([]int, error) {
	pids, err := userPIDs()
	if err != nil {
		return nil, err
	}
	var out []int
	for _, pid := range pids {
		if p.WrittenBy(pid) {
			out = append(out, pid)
		}
	}
	return out, nil
}

// WrittenBy reports whether pid holds p's write end open right now. A process that has
// exited, or that this user may not inspect, is not a writer.
func (p Pipe) WrittenBy(pid int) bool {
	for _, fd := range pipeFDs(pid) {
		if h, _, ok := pipeHandles(pid, fd); ok && h == p.id {
			return true
		}
	}
	return false
}

// Args returns pid's argument vector, argv[0] first.
func Args(pid int) ([]string, error) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, fmt.Errorf("pipepeer: procargs of %d: %w", pid, err)
	}
	// argc, then the exec path and its NUL padding, then argc NUL-terminated strings.
	if len(raw) < 4 {
		return nil, fmt.Errorf("pipepeer: short procargs for %d", pid)
	}
	argc := int(binary.LittleEndian.Uint32(raw))
	rest := raw[4:]
	i := bytes.IndexByte(rest, 0)
	if i < 0 {
		return nil, fmt.Errorf("pipepeer: malformed procargs for %d", pid)
	}
	rest = bytes.TrimLeft(rest[i:], "\x00")
	args := make([]string, 0, argc)
	for len(args) < argc && len(rest) > 0 {
		j := bytes.IndexByte(rest, 0)
		if j < 0 {
			j = len(rest)
		}
		args = append(args, string(rest[:j]))
		rest = rest[min(j+1, len(rest)):]
	}
	return args, nil
}

// Parent returns pid's parent process id.
func Parent(pid int) (int, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, fmt.Errorf("pipepeer: kinfo of %d: %w", pid, err)
	}
	if kp.Proc.P_pid != int32(pid) {
		return 0, fmt.Errorf("pipepeer: no process %d", pid)
	}
	return int(kp.Eproc.Ppid), nil
}

func executable(pid int) (os.FileInfo, error) {
	buf := make([]byte, procPIDPathBufLen)
	// The kernel returns 0 here rather than a length; libproc's proc_pidpath measures the
	// NUL-terminated result itself.
	if _, err := procInfo(procInfoCallPIDInfo, pid, procPIDPathInfo, 0, buf); err != nil {
		return nil, fmt.Errorf("pipepeer: path of %d: %w", pid, err)
	}
	path := buf
	if i := bytes.IndexByte(path, 0); i >= 0 {
		path = path[:i]
	}
	return os.Stat(string(path))
}

// pipeHandles returns the kernel handles of the pipe end open at fd in pid, and of its
// peer end, or false when fd is not a pipe.
func pipeHandles(pid, fd int) (self, peer uint64, ok bool) {
	buf := make([]byte, pipeFDInfoSize)
	n, err := procInfo(procInfoCallPIDFDInfo, pid, procPIDFDPipeInfo, uint64(fd), buf)
	if err != nil || n < pipeFDInfoSize {
		return 0, 0, false
	}
	return binary.LittleEndian.Uint64(buf[pipeHandleOffset:]), binary.LittleEndian.Uint64(buf[pipePeerOffset:]), true
}

// pipeFDs lists pid's descriptors that are pipes.
func pipeFDs(pid int) []int {
	// A zero-length query reports the size the table needs; the slack covers a
	// descriptor opened between the two calls.
	need, err := procInfo(procInfoCallPIDInfo, pid, procPIDListFDs, 0, nil)
	if err != nil || need <= 0 {
		return nil
	}
	buf := make([]byte, need+32*procFDInfoSize)
	n, err := procInfo(procInfoCallPIDInfo, pid, procPIDListFDs, 0, buf)
	if err != nil {
		return nil
	}
	var fds []int
	for off := 0; off+procFDInfoSize <= n; off += procFDInfoSize {
		if binary.LittleEndian.Uint32(buf[off+4:]) == proxFDTypePipe {
			fds = append(fds, int(int32(binary.LittleEndian.Uint32(buf[off:]))))
		}
	}
	return fds
}

// userPIDs lists this user's processes.
func userPIDs() ([]int, error) {
	need, err := procInfo(procInfoCallListPIDs, procUIDOnly, os.Getuid(), 0, nil)
	if err != nil {
		return nil, fmt.Errorf("pipepeer: list pids: %w", err)
	}
	buf := make([]byte, need+64*4)
	n, err := procInfo(procInfoCallListPIDs, procUIDOnly, os.Getuid(), 0, buf)
	if err != nil {
		return nil, fmt.Errorf("pipepeer: list pids: %w", err)
	}
	var pids []int
	for off := 0; off+4 <= n; off += 4 {
		if pid := int(int32(binary.LittleEndian.Uint32(buf[off:]))); pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// procInfo is __proc_info(callnum, pid, flavor, arg, buffer, buffersize). For
// LISTPIDS the pid and flavor slots carry the list type and its argument.
func procInfo(call, pid, flavor int, arg uint64, buf []byte) (int, error) {
	var p unsafe.Pointer
	if len(buf) > 0 {
		p = unsafe.Pointer(&buf[0])
	}
	// libproc's wrappers would need cgo or hand-written trampolines. The syscall is what
	// they call, and x/sys routes it through libSystem's syscall(2) all the same.
	r, _, errno := unix.Syscall6(unix.SYS_PROC_INFO, //nolint:staticcheck // SA1019: see above
		uintptr(call), uintptr(pid), uintptr(flavor), uintptr(arg), uintptr(p), uintptr(len(buf)))
	if errno != 0 {
		return 0, errno
	}
	return int(r), nil
}
