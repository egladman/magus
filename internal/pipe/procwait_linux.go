//go:build linux

package pipe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/egladman/magus/internal/log/attr"
)

// awaitExit blocks until one of pids has exited (a pid already gone counts) or ctx ends.
// A pidfd polls readable once its process exits, child of this one or not; closing the
// wake pipe is how ctx's end wakes the poll.
func awaitExit(ctx context.Context, pids []int) error {
	var wake [2]int
	if err := unix.Pipe2(wake[:], unix.O_CLOEXEC); err != nil {
		return fmt.Errorf("pipe: wake pipe: %w", err)
	}
	fds := make([]unix.PollFd, 0, len(pids)+1)
	fds = append(fds, unix.PollFd{Fd: int32(wake[0]), Events: unix.POLLIN})
	defer func() {
		for _, fd := range fds {
			_ = unix.Close(int(fd.Fd))
		}
	}()
	for _, pid := range pids {
		fd, err := unix.PidfdOpen(pid, 0)
		if err != nil {
			_ = unix.Close(wake[1])
			switch {
			case errors.Is(err, unix.ESRCH):
				return nil
			case errors.Is(err, unix.ENOSYS):
				return awaitTick(ctx)
			}
			return fmt.Errorf("pipe: pidfd_open %d: %w", pid, err)
		}
		fds = append(fds, unix.PollFd{Fd: int32(fd), Events: unix.POLLIN})
	}

	fired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(fired)
		_ = unix.Close(wake[1])
	})
	defer func() {
		if stop() {
			_ = unix.Close(wake[1])
		} else {
			<-fired
		}
	}()
	for {
		_, err := unix.Poll(fds, -1)
		switch {
		case errors.Is(err, unix.EINTR):
			continue
		case err != nil:
			return fmt.Errorf("pipe: poll: %w", err)
		case fds[0].Revents != 0:
			return ctx.Err()
		}
		return nil
	}
}

// pollEvery paces the wait on a kernel without pidfd_open (before Linux 5.3).
const pollEvery = 100 * time.Millisecond

var saidNoPidfd sync.Once

// awaitTick waits one pollEvery for a caller that then checks its stages again. It says
// once that it does, since a wait that wakes ten times a second is worth knowing about.
// Copied in procwait_other.go: darwin never needs it, and a shared copy is unused there.
func awaitTick(ctx context.Context) error {
	saidNoPidfd.Do(func() {
		slog.InfoContext(ctx, fmt.Sprintf("this kernel has no pidfd_open (Linux 5.3+), so a wait on an upstream pipe stage checks every %s", pollEvery),
			attr.Component("magus"))
	})
	t := time.NewTimer(pollEvery)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
