//go:build linux || darwin

package broker

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// writeWithFiles sends line in one sendmsg with files attached, so the broker receives
// them with the frame's first byte.
//
// Each descriptor is read through SyscallConn rather than Fd: Fd puts a file into
// blocking mode, which would change how this process writes its own stderr.
func writeWithFiles(w io.Writer, line []byte, files []*os.File) error {
	uc, ok := w.(*net.UnixConn)
	if !ok {
		return errNoFilePassing
	}
	fds := make([]int, 0, len(files))
	for _, f := range files {
		rc, err := f.SyscallConn()
		if err != nil {
			return fmt.Errorf("broker: pass %s: %w", f.Name(), err)
		}
		if err := rc.Control(func(fd uintptr) { fds = append(fds, int(fd)) }); err != nil {
			return fmt.Errorf("broker: pass %s: %w", f.Name(), err)
		}
	}
	n, _, err := uc.WriteMsgUnix(line, unix.UnixRights(fds...), nil)
	if err == nil && n < len(line) {
		_, err = uc.Write(line[n:])
	}
	return err
}

// rightsReader reads a unix connection with recvmsg, keeping the files a peer passes
// beside its frames, which a plain read would close unseen.
type rightsReader struct {
	rc  syscall.RawConn
	oob []byte

	mu    sync.Mutex
	files []*os.File
}

// newConnReader returns the reader a server reads c through and a func that hands over
// the files c has passed so far, each once.
func newConnReader(c net.Conn) (io.Reader, func() []*os.File) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return c, func() []*os.File { return nil }
	}
	rc, err := uc.SyscallConn()
	if err != nil {
		return c, func() []*os.File { return nil }
	}
	r := &rightsReader{rc: rc, oob: make([]byte, unix.CmsgSpace(4*maxPassedFiles))}
	return r, r.take
}

func (r *rightsReader) Read(p []byte) (int, error) {
	var n, oobn, flags int
	var rerr error
	err := r.rc.Read(func(fd uintptr) bool {
		n, oobn, flags, _, rerr = unix.Recvmsg(int(fd), p, r.oob, recvFlags)
		return rerr != unix.EAGAIN
	})
	if err == nil {
		err = rerr
	}
	// recvmsg reports a failure as -1, which an io.Reader may never return.
	n = max(n, 0)
	if oobn > 0 {
		r.keep(r.oob[:oobn])
	}
	if err != nil {
		return n, err
	}
	if flags&unix.MSG_CTRUNC != 0 {
		return n, errors.New("broker: a peer passed more file descriptors than a frame carries")
	}
	if n == 0 && len(p) > 0 {
		return 0, io.EOF
	}
	return n, nil
}

func (r *rightsReader) keep(oob []byte) {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return
	}
	var got []*os.File
	for i := range msgs {
		fds, err := unix.ParseUnixRights(&msgs[i])
		if err != nil {
			continue
		}
		for _, fd := range fds {
			unix.CloseOnExec(fd)
			got = append(got, os.NewFile(uintptr(fd), "passed"))
		}
	}
	r.mu.Lock()
	r.files = append(r.files, got...)
	r.mu.Unlock()
}

func (r *rightsReader) take() []*os.File {
	r.mu.Lock()
	defer r.mu.Unlock()
	files := r.files
	r.files = nil
	return files
}
