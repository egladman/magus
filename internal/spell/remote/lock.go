package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/opencontainers/go-digest"
	"gopkg.in/yaml.v3"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/spells"
)

// LockFile is the lock's name. It sits beside magus.yaml with the same stem, as go.sum
// sits beside go.mod.
const LockFile = "magus.lock"

// lockVersion is the schema LockFile is written in. A lock in any other version is
// refused by name rather than read field by field.
const lockVersion = 1

// lockHeader opens every written lock, so a reader of the diff knows not to edit it.
const lockHeader = "# Written by `magus spell lock`. Do not edit: change a tag in magus.yaml and run the update charm.\n"

// Lock is magus.lock: the manifest digest each declared remote spell's tag resolved to
// when the update charm last ran. It is the supply-chain record, so reviewing an
// upgrade means reviewing its diff.
type Lock struct {
	Version int                  `yaml:"version"`
	Spells  map[string]LockEntry `yaml:"spells,omitempty"`
}

// LockEntry pins one remote spell, keyed in Lock.Spells by its import path. Tag is the
// tag Digest was resolved from; a declaration whose tag no longer matches is caught
// instead of being served bytes an older tag named.
type LockEntry struct {
	Tag    string        `yaml:"tag"`
	Digest digest.Digest `yaml:"digest"`
}

// ReadLock reads root's magus.lock. A workspace with no lock reads as an empty one, so
// a declaration against it reports a missing entry rather than a missing file.
func ReadLock(root string) (Lock, error) {
	raw, err := os.ReadFile(filepath.Join(root, LockFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Lock{Version: lockVersion}, nil
	}
	if err != nil {
		return Lock{}, err
	}
	return parseLock(raw)
}

func parseLock(raw []byte) (Lock, error) {
	var l Lock
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&l); err != nil && !errors.Is(err, io.EOF) {
		return Lock{}, fmt.Errorf("%s: %w", LockFile, err)
	}
	if l.Version != lockVersion {
		return Lock{}, fmt.Errorf("%s: schema version %d, this magus reads version %d; rewrite it with `magus spell lock --update`",
			LockFile, l.Version, lockVersion)
	}
	for path, e := range l.Spells {
		if !spells.IsRemoteImport(path) {
			return Lock{}, fmt.Errorf("%s: %q is not a registry path", LockFile, path)
		}
		if err := e.Digest.Validate(); err != nil {
			return Lock{}, fmt.Errorf("%s: %s: %w", LockFile, path, err)
		}
	}
	return l, nil
}

// Marshal renders l as the lock file's bytes: the header, then YAML with its keys
// sorted, so the same pins always write the same bytes and a diff shows only what moved.
func (l Lock) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(lockHeader)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(l); err != nil {
		return nil, fmt.Errorf("%s: %w", LockFile, err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("%s: %w", LockFile, err)
	}
	return buf.Bytes(), nil
}

// UpdateLock rewrites root's magus.lock with what next returns for the current lock,
// holding an OS file lock so two updates cannot interleave their read and write. The
// flock lives under .magus/, not beside magus.lock, because it is never committed.
//
// A result that pins nothing removes magus.lock rather than writing one: a workspace
// with no remote spells has no lock, which ReadLock already reads as an empty one.
func UpdateLock(ctx context.Context, root string, next func(Lock) (Lock, error)) error {
	dir := filepath.Join(root, ".magus")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	fl := flock.New(filepath.Join(dir, LockFile+".flock"))
	got, err := fl.TryLock()
	if err != nil {
		return fmt.Errorf("%s: lock %s: %w", LockFile, fl.Path(), err)
	}
	if !got {
		wait, cancel := context.WithTimeout(ctx, lockWait)
		defer cancel()
		if got, err = fl.TryLockContext(wait, lockRetryDelay); err != nil || !got {
			if ctx.Err() != nil {
				return fmt.Errorf("%s: cancelled while waiting for %s, so nothing was written: %w", LockFile, fl.Path(), ctx.Err())
			}
			return fmt.Errorf("%s: another process has held %s for more than %s, so nothing was written;"+
				" look for a stuck magus process with `magus status`, then retry", LockFile, fl.Path(), lockWait)
		}
	}
	defer func() { _ = fl.Unlock() }()

	cur, err := ReadLock(root)
	if err != nil {
		return err
	}
	l, err := next(cur)
	if err != nil {
		return err
	}
	path := filepath.Join(root, LockFile)
	if len(l.Spells) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	raw, err := l.Marshal()
	if err != nil {
		return err
	}
	return file.WriteFileAtomic(path, raw, 0o644)
}

// The file lock's shape, matching the job store's: a lock rewrite is a small file, so
// a wait past lockWait means the holder is stuck rather than busy.
const (
	lockWait       = 10 * time.Second
	lockRetryDelay = 20 * time.Millisecond
)
