//go:build unix

package settle

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// silenced runs fn with this process's stdout and stderr pointed at a temporary file and
// returns what landed there. The descriptors themselves are swapped (dup2), so slog's
// handler, a writer that captured os.Stderr earlier and every child fn starts are all
// silenced, which swapping the os.Stdout variable would not do. Whatever another
// goroutine writes meanwhile lands in the file too.
func silenced(fn func() error) ([]byte, error) {
	f, err := os.CreateTemp("", "magus-settle-*.log")
	if err != nil {
		return nil, fn()
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()
	saved := [3]int{-1, -1, -1}
	for _, std := range []int{1, 2} {
		if saved[std], err = unix.Dup(std); err != nil {
			_ = restore(saved)
			return nil, fn()
		}
		if err := unix.Dup2(int(f.Fd()), std); err != nil {
			_ = restore(saved)
			return nil, fn()
		}
	}
	runErr := fn()
	if err := restore(saved); err != nil {
		return nil, errors.Join(runErr, err)
	}
	captured, err := os.ReadFile(f.Name())
	if err != nil {
		return nil, runErr
	}
	return captured, runErr
}

// restore points fds 1 and 2 back at the descriptors saved for them and closes the copies.
// A -1 entry was never saved.
func restore(saved [3]int) error {
	var errs []error
	for _, std := range []int{1, 2} {
		if saved[std] < 0 {
			continue
		}
		if err := unix.Dup2(saved[std], std); err != nil {
			errs = append(errs, fmt.Errorf("restore fd %d: %w", std, err))
		}
		_ = unix.Close(saved[std])
	}
	return errors.Join(errs...)
}
