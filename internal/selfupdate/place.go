package selfupdate

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// ShorthandName is the de facto short name for magus. Every shipped completion
// script already registers it alongside magus, so the symlink is the only part
// of the shorthand a user still has to set up by hand.
const ShorthandName = "mgs"

// Placed is what Place put where.
type Placed struct {
	Commit    string
	BuildDate string
	Path      string
}

// Place replaces the running binary with source, which must report a clean commit
// stamp newer than the running build. The target gets a new inode: on macOS an
// in-place rewrite leaves a stale code signature and the next exec is killed.
func Place(ctx context.Context, source string) (Placed, error) {
	repl, err := readPlaceStamp(ctx, source)
	if err != nil {
		return Placed{}, err
	}
	if err := types.MagusBuildFromContext(ctx).AcceptsReplacement(repl); err != nil {
		return Placed{}, fmt.Errorf("magus self place: %w", err)
	}
	target, err := RunningBinary()
	if err != nil {
		return Placed{}, err
	}
	if err := placeInto(source, target); err != nil {
		return Placed{}, fmt.Errorf("magus self place: %w", err)
	}
	return Placed{Commit: repl.Commit, BuildDate: repl.BuildDate, Path: target}, nil
}

// readPlaceStamp asks source for its build stamp.
func readPlaceStamp(ctx context.Context, source string) (types.BuildStamp, error) {
	var stamp types.BuildStamp
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, source, "version", "-o", "json")
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return stamp, fmt.Errorf("magus self place: %s did not report its version: %w", source, err)
	}
	if err := json.Unmarshal(out.Bytes(), &stamp); err != nil {
		return stamp, fmt.Errorf("magus self place: %s reported a version this magus cannot read: %w", source, err)
	}
	return stamp, nil
}

// placeInto writes source's bytes over target as a new file.
func placeInto(source, target string) error {
	body, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return file.WriteFileAtomic(target, body, 0o755)
}

// RunningBinary returns the real path of the running binary. os.Executable
// reports the as-invoked path on macOS, so running this through an existing mgs
// link would otherwise point the new link at a link. EvalSymlinks pins it to the
// real file. It does not borrow ResolveTargetPath, which only the updater's build
// compiles in.
func RunningBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return exe, nil
}

// Shorthand is the link InstallShorthand left or found.
type Shorthand struct {
	Link    string
	Target  string
	Created bool
}

// InstallShorthand symlinks the shorthand to the running binary.
//
// A symlink beats a shell alias because it also resolves in scripts, Makefiles,
// and anything else that is not an interactive shell. It survives an update:
// selfupdate resolves symlinks before it swaps, so the real binary is replaced
// underneath a link that keeps pointing at it. dir empty means beside the binary.
func InstallShorthand(dir string, force bool) (Shorthand, error) {
	target, err := RunningBinary()
	if err != nil {
		return Shorthand{}, fmt.Errorf("magus self install-shorthand: %w", err)
	}

	name := ShorthandName
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	linkDir := dir
	if linkDir == "" {
		linkDir = filepath.Dir(target)
	}
	linkPath := filepath.Join(linkDir, name)

	if _, err := os.Lstat(linkPath); err == nil {
		if resolved, err := filepath.EvalSymlinks(linkPath); err == nil && resolved == target {
			return Shorthand{Link: linkPath, Target: target}, nil
		}
		if !force {
			return Shorthand{}, fmt.Errorf("%s already exists (use --force to replace it)", linkPath)
		}
		if err := os.Remove(linkPath); err != nil {
			return Shorthand{}, fmt.Errorf("magus self install-shorthand: replace %s: %w", linkPath, err)
		}
	}

	if err := os.Symlink(target, linkPath); err != nil {
		if runtime.GOOS == "windows" {
			return Shorthand{}, fmt.Errorf("magus self install-shorthand: create %s (Windows creates symlinks only in Developer Mode or an elevated shell): %w", linkPath, err)
		}
		return Shorthand{}, fmt.Errorf("magus self install-shorthand: create %s: %w", linkPath, err)
	}
	return Shorthand{Link: linkPath, Target: target, Created: true}, nil
}
