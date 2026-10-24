//go:build darwin

package pipe

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// awaitExit blocks until one of pids has exited (a pid already gone counts) or ctx ends.
// EVFILT_PROC watches a process that is not this one's child, so the wait costs nothing;
// EVFILT_USER is how ctx's end wakes it.
func awaitExit(ctx context.Context, pids []int) error {
	kq, err := unix.Kqueue()
	if err != nil {
		return fmt.Errorf("pipe: kqueue: %w", err)
	}
	defer func() { _ = unix.Close(kq) }()

	const wakeIdent = 0
	if _, err := unix.Kevent(kq, []unix.Kevent_t{{Ident: wakeIdent, Filter: unix.EVFILT_USER, Flags: unix.EV_ADD | unix.EV_CLEAR}}, nil, nil); err != nil {
		return fmt.Errorf("pipe: kevent: %w", err)
	}
	for _, pid := range pids {
		watch := unix.Kevent_t{Ident: uint64(pid), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT}
		_, err := unix.Kevent(kq, []unix.Kevent_t{watch}, nil, nil)
		if errors.Is(err, unix.ESRCH) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("pipe: watch pid %d: %w", pid, err)
		}
	}

	// The trigger must finish before kq closes, or it could reach a descriptor reused since.
	fired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(fired)
		_, _ = unix.Kevent(kq, []unix.Kevent_t{{Ident: wakeIdent, Filter: unix.EVFILT_USER, Fflags: unix.NOTE_TRIGGER}}, nil, nil)
	})
	defer func() {
		if !stop() {
			<-fired
		}
	}()

	events := make([]unix.Kevent_t, 1)
	for {
		n, err := unix.Kevent(kq, nil, events, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return fmt.Errorf("pipe: kevent: %w", err)
		}
		if n == 0 {
			continue
		}
		if events[0].Filter == unix.EVFILT_USER {
			return ctx.Err()
		}
		return nil
	}
}
