package spell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/vcs"
)

// ResolveInstall picks the install a project at projectDir runs: the first of the
// spec's manifests present there, then the nearest lock candidate walking up to
// stopDir. found is false when there is nothing to install: no manifest, a manifest
// that declares no installs (setup.py), or no lockfile, which pins nothing.
//
// A lockfile the spell declares no install for is an error. Running some other
// manager's command against it would rewrite or ignore it.
func ResolveInstall(spec *spells.InstallSpec, projectDir, stopDir string) (choice spells.InstallChoice, found bool, err error) {
	if spec == nil {
		return choice, false, nil
	}
	for _, man := range spec.Manifests {
		manifest := filepath.Join(projectDir, man.Value)
		if !isRegular(manifest) {
			continue
		}
		if len(man.Installs) == 0 {
			return choice, false, nil
		}
		for dir := projectDir; ; dir = filepath.Dir(dir) {
			for _, lock := range man.LockCandidates {
				path := filepath.Join(dir, lock)
				if !isRegular(path) {
					continue
				}
				in, ok := man.Installs[lock]
				if !ok {
					return choice, false, fmt.Errorf("spell %q declares no install for %s; install it with its own package manager", spec.Spell, path)
				}
				return spells.InstallChoice{Manifest: manifest, Lock: path, Install: in}, true, nil
			}
			if dir == stopDir || filepath.Dir(dir) == dir {
				break
			}
		}
		return choice, false, nil
	}
	return choice, false, nil
}

func isRegular(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// seedSuffix marks a clone in flight beside its destination. It carries the pid for
// debugging only; a reused pid must not decide whether the seed is abandoned, so the
// actual liveness signal is the flock on its companion seedLockSuffix file (see
// acquireSeedLock), held for exactly as long as the clone runs.
const seedSuffix = ".magus-seed-"

// seedLockSuffix names the lock file that accompanies a seed directory in flight. The OS
// releases the lock the instant the holding process exits for any reason, including a
// crash, so a later magus can tell an abandoned seed from a live one without trusting a
// pid that may since have been reused by an unrelated process.
const seedLockSuffix = ".lock"

// SeedInstall clones the install's dependency directory from another checkout of the
// same repository when it is absent here, so the install that follows reconciles a
// near-complete tree instead of building one. found follows [ResolveInstall]'s
// convention: false means nothing was seeded, whatever the reason, and from is only
// meaningful when it is true.
//
// Seeding is an optimization and never a verdict: every failure is returned for the
// caller to report, and the install runs either way. It never writes over an existing
// directory, and a clone becomes visible only by an exclusive rename, so an
// interrupted seed never leaves a partial tree where the package manager would trust it.
func SeedInstall(ctx context.Context, choice spells.InstallChoice, projectDir string) (from string, found bool, err error) {
	in := choice.Install
	if !in.Relocatable || in.Dir == "" || !cloneSupported {
		return "", false, nil
	}
	dst := filepath.Join(projectDir, in.Dir)
	if _, err := os.Lstat(dst); !errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	root, others, err := vcs.Checkouts(projectDir)
	if err != nil || root == "" {
		return "", false, err
	}
	rel, err := filepath.Rel(root, dst)
	if err != nil {
		return "", false, err
	}
	// A sibling failing to clone (permissions, a mid-clone removal, disk pressure) does
	// not mean the tree is unseedable, only that this one is; the next sibling still
	// might work, so failures accumulate rather than stopping the search.
	var errs []error
	for _, co := range sameLockFirst(root, choice.Lock, others) {
		src := filepath.Join(co, rel)
		if fi, err := os.Lstat(src); err != nil || !fi.IsDir() {
			continue
		}
		removeAbandonedSeeds(ctx, dst)
		tmp := dst + seedSuffix + strconv.Itoa(os.Getpid())
		lock, err := acquireSeedLock(tmp + seedLockSuffix)
		if err != nil {
			errs = append(errs, fmt.Errorf("lock %s: %w", tmp, err))
			continue
		}
		cleanup := func() {
			_ = lock.Close()
			_ = os.Remove(tmp + seedLockSuffix)
		}
		if err := cloneTree(src, tmp); err != nil {
			_ = os.RemoveAll(tmp)
			cleanup()
			errs = append(errs, fmt.Errorf("clone %s: %w", src, err))
			continue
		}
		if err := renameExclusive(tmp, dst); err != nil {
			_ = os.RemoveAll(tmp)
			cleanup()
			if errors.Is(err, fs.ErrExist) {
				return "", false, nil
			}
			return "", false, err
		}
		cleanup()
		return co, true, nil
	}
	return "", false, errors.Join(errs...)
}

// sameLockFirst orders the checkouts whose copy of lock is byte-identical to this one
// ahead of the rest, each group in its given order. A tree installed from the same lock
// leaves the package manager nothing to reconcile, where the first checkout found may be
// a branch whose lock is months behind. lock is absolute, under root.
func sameLockFirst(root, lock string, others []string) []string {
	rel, err := filepath.Rel(root, lock)
	if err != nil {
		return others
	}
	mine, err := os.ReadFile(lock)
	if err != nil {
		return others
	}
	same := make([]string, 0, len(others))
	var rest []string
	for _, co := range others {
		theirs := filepath.Join(co, rel)
		// Size first: most checkouts on another lock differ in length, and a stat is far
		// cheaper than reading a lockfile hundreds of times.
		if fi, err := os.Stat(theirs); err == nil && fi.Size() == int64(len(mine)) {
			if b, err := os.ReadFile(theirs); err == nil && bytes.Equal(b, mine) {
				same = append(same, co)
				continue
			}
		}
		rest = append(rest, co)
	}
	return append(same, rest...)
}

// removeAbandonedSeeds deletes clones a killed magus left beside dst. A seed whose lock
// is still held belongs to a live process and is left alone; see seedLockSuffix.
func removeAbandonedSeeds(ctx context.Context, dst string) {
	matches, _ := filepath.Glob(dst + seedSuffix + "*")
	for _, m := range matches {
		if strings.HasSuffix(m, seedLockSuffix) {
			continue // the lock file itself, not the seed directory
		}
		if fi, err := os.Lstat(m); err != nil || !fi.IsDir() {
			continue
		}
		if !seedAbandoned(m + seedLockSuffix) {
			continue
		}
		if err := os.RemoveAll(m); err != nil {
			slog.DebugContext(ctx, "spell: could not remove an abandoned seed", "path", m, "err", err)
			continue
		}
		_ = os.Remove(m + seedLockSuffix)
	}
}
