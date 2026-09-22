package spell

import (
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

// seedSuffix marks a clone in flight beside its destination. It carries the pid so a
// later seed can tell an abandoned one from a live one.
const seedSuffix = ".magus-seed-"

// SeedInstall clones the install's dependency directory from another checkout of the
// same repository when it is absent here, so the install that follows reconciles a
// near-complete tree instead of building one. It returns the checkout it seeded from,
// or "" when it did not seed.
//
// Seeding is an optimization and never a verdict: every failure is returned for the
// caller to report, and the install runs either way. It never writes over an existing
// directory, and a clone becomes visible only by an exclusive rename, so an
// interrupted seed never leaves a partial tree where the package manager would trust it.
func SeedInstall(ctx context.Context, choice spells.InstallChoice, projectDir string) (string, error) {
	in := choice.Install
	if !in.Relocatable || in.Dir == "" || !cloneSupported {
		return "", nil
	}
	dst := filepath.Join(projectDir, in.Dir)
	if _, err := os.Lstat(dst); !errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	root, others, err := vcs.Checkouts(projectDir)
	if err != nil || root == "" {
		return "", err
	}
	rel, err := filepath.Rel(root, dst)
	if err != nil {
		return "", err
	}
	for _, co := range others {
		src := filepath.Join(co, rel)
		if fi, err := os.Lstat(src); err != nil || !fi.IsDir() {
			continue
		}
		removeAbandonedSeeds(ctx, dst)
		tmp := dst + seedSuffix + strconv.Itoa(os.Getpid())
		if err := cloneTree(src, tmp); err != nil {
			_ = os.RemoveAll(tmp)
			return "", fmt.Errorf("clone %s: %w", src, err)
		}
		if err := renameExclusive(tmp, dst); err != nil {
			_ = os.RemoveAll(tmp)
			if errors.Is(err, fs.ErrExist) {
				return "", nil
			}
			return "", err
		}
		return co, nil
	}
	return "", nil
}

// removeAbandonedSeeds deletes clones a killed magus left beside dst. A seed whose pid
// is still running belongs to a live process and is left alone.
func removeAbandonedSeeds(ctx context.Context, dst string) {
	matches, _ := filepath.Glob(dst + seedSuffix + "*")
	for _, m := range matches {
		pid, err := strconv.Atoi(strings.TrimPrefix(m, dst+seedSuffix))
		if err != nil || pid <= 0 || (pid != os.Getpid() && processAlive(pid)) {
			continue
		}
		if err := os.RemoveAll(m); err != nil {
			slog.DebugContext(ctx, "spell: could not remove an abandoned seed", "path", m, "err", err)
		}
	}
}
