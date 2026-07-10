package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/egladman/magus/types"
)

// tailCmd dispatches `magus tail`: stream the captured log of the most
// recent cache entry for a project.
func tailCmd(ctx context.Context, root string, args []string) error {
	var (
		follow bool
		lines  int
	)
	rest, err := cmdParse("tail", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&follow, "f", false, "follow: print last -n lines then stream appended output")
		fs.IntVar(&lines, "n", 10, "number of lines to print (0 = entire file)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus tail [-f] [-n N] [target]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Stream the captured build log of the most recent cache entry for a")
			fmt.Fprintln(os.Stderr, "project. The log was written during a cache miss (when the build")
			fmt.Fprintln(os.Stderr, "actually ran). Requires an interactive terminal.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "A convenience for the LATEST log of a project/target, with -f follow. For")
			fmt.Fprintln(os.Stderr, "a SPECIFIC past execution's exact output (any target, by the ref shown on")
			fmt.Fprintln(os.Stderr, "its run line), use `magus query ref<hex>` - see `magus query -h`.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "target accepts the canonical path:target form used by `magus run`:")
			fmt.Fprintln(os.Stderr, "  (none)          cwd project, latest run of any target")
			fmt.Fprintln(os.Stderr, "  :build          cwd project, latest build run")
			fmt.Fprintln(os.Stderr, "  api             api project, latest run of any target")
			fmt.Fprintln(os.Stderr, "  api:test        api project, latest test run")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

	if !isInteractiveTTY() && !globalCfg.AssumeInteractive {
		fmt.Fprintln(os.Stderr, "magus: tail requires an interactive terminal; pipe the output instead")
		fmt.Fprintln(os.Stderr, "       (set assume_interactive: true in magus.yaml or MAGUS_ASSUME_INTERACTIVE=1 to override)")
		return errSilent{exitCode: 2}
	}

	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	projectPath, targetName, err := resolveTailTarget(m, rest)
	if err != nil {
		return err
	}

	logPath, err := m.TailLog(projectPath, targetName)
	if errors.Is(err, fs.ErrNotExist) {
		if targetName != "" {
			return fmt.Errorf("magus tail: no cache entries for %q with target %q — run a build first", projectPath, targetName)
		}
		return fmt.Errorf("magus tail: no cache entries found for project %q — run a build first", projectPath)
	}
	if err != nil {
		return fmt.Errorf("magus tail: %w", err)
	}

	f, err := os.Open(filepath.Clean(logPath))
	if err != nil {
		return fmt.Errorf("magus tail: open log: %w", err)
	}
	defer f.Close()

	if err := printTail(f, lines); err != nil {
		return err
	}
	if follow {
		return followLog(ctx, f)
	}
	return nil
}

// resolveTailTarget parses the optional positional arguments (<target> [project])
// and resolves the project path. With no argument it falls back to cwd.
func resolveTailTarget(ws types.WorkspaceRepository, args []string) (projectPath, targetName string, err error) {
	if len(args) == 0 {
		targets, found, err := ws.ExpandCwd(types.Target{})
		if err != nil {
			return "", "", err
		}
		if !found {
			return "", "", errors.New("magus tail: not inside a project directory")
		}
		return targets[0].Path, "", nil
	}

	t, err := types.ParseTarget(args[0])
	if err != nil {
		return "", "", fmt.Errorf("magus tail: %w", err)
	}
	if len(args) > 1 {
		t.Path = args[1]
	}

	if t.Path == "" {
		// Bare target or :target sugar — resolve path from cwd.
		targets, found, err := ws.ExpandCwd(types.Target{})
		if err != nil {
			return "", "", err
		}
		if !found {
			return "", "", errors.New("magus tail: not inside a project directory")
		}
		return targets[0].Path, t.Name, nil
	}

	// Explicit path: validate it exists in the workspace.
	expanded, err := ws.ExpandPath(t)
	if err != nil {
		return "", "", fmt.Errorf("magus tail: %w", err)
	}
	if len(expanded) == 0 {
		return "", "", fmt.Errorf("magus tail: project %q not found in workspace", t.Path)
	}
	return expanded[0].Path, t.Name, nil
}

// printTail reads the entire file and writes the last n lines to stdout.
// n == 0 writes the whole file.
func printTail(f *os.File, n int) error {
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	out := tailLines(data, n)
	_, err = os.Stdout.Write(out)
	return err
}

// tailLines returns the last n newline-terminated lines from data.
// n == 0 returns data unchanged.
func tailLines(data []byte, n int) []byte {
	if n == 0 || len(data) == 0 {
		return data
	}
	trailingNewline := len(data) > 0 && data[len(data)-1] == '\n'
	// Strip a single trailing newline to avoid counting an empty final "line".
	trimmed := data
	if trailingNewline {
		trimmed = trimmed[:len(trimmed)-1]
	}
	parts := bytes.Split(trimmed, []byte{'\n'})
	if n >= len(parts) {
		return data
	}
	out := bytes.Join(parts[len(parts)-n:], []byte{'\n'})
	if trailingNewline {
		out = append(out, '\n')
	}
	return out
}

// followLog streams new bytes appended to f until ctx is cancelled.
func followLog(ctx context.Context, f *os.File) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := io.Copy(os.Stdout, f); err != nil {
				return err
			}
		}
	}
}
