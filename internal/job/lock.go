package job

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/internal/file"
)

// locked runs fn holding the OS file lock beside jobs.json, waiting at most
// [file.LockWait]. The holder writes its pid, start time and command line into the lock
// file, so a caller that gives up names who it waited on.
func (s *Store) locked(ctx context.Context, fn func() error) error {
	path := s.path + ".lock"
	ran := false
	err := file.WithLock(ctx, path, file.LockWait, func() error {
		ran = true
		// In place: a rename would swap the inode the flock is held on.
		_ = os.WriteFile(path, []byte(lockHolder(time.Now())), 0o600)
		return fn()
	})
	if err == nil || ran {
		return err
	}
	if holder := readLockHolder(path, time.Now()); holder != "" {
		return fmt.Errorf("job: the holder is %s: %w", holder, err)
	}
	return fmt.Errorf("job: %w", err)
}

// lockHolder is the lock file's content: "<pid> <unix seconds> <argv joined by spaces>".
func lockHolder(now time.Time) string {
	return fmt.Sprintf("%d %d %s\n", os.Getpid(), now.Unix(), strings.Join(os.Args, " "))
}

// readLockHolder names the process that last took the lock at path, "" when the file
// names none, as a lock taken by a magus that predates the record leaves it.
func readLockHolder(path string, now time.Time) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	f := strings.SplitN(strings.TrimSpace(string(raw)), " ", 3)
	if len(f) < 3 {
		return ""
	}
	pid, perr := strconv.Atoi(f[0])
	at, aerr := strconv.ParseInt(f[1], 10, 64)
	if perr != nil || aerr != nil {
		return ""
	}
	return fmt.Sprintf("pid %d (`%s`), which took it %s ago", pid, f[2], now.Sub(time.Unix(at, 0)).Round(time.Second))
}
