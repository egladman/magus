package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// placeStamp is the part of `version -o json` that names a build.
type placeStamp struct {
	Commit    string `json:"commit"`
	Dirty     bool   `json:"dirty"`
	BuildDate string `json:"build_date"`
}

// selfPlaceCmd implements `magus self place <binary>`. The target gets a new inode:
// on macOS an in-place rewrite leaves a stale code signature and the next exec is killed.
func selfPlaceCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("self place", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.SetOutput(os.Stderr)
	fs.Usage = selfPlaceUsage
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("magus self place: want one binary to place, got %d arguments", fs.NArg())
	}
	source := fs.Arg(0)

	repl, err := readPlaceStamp(ctx, source)
	if err != nil {
		return err
	}
	if err := checkReplacement(types.MagusBuildFromContext(ctx), repl); err != nil {
		return err
	}
	target, err := runningBinaryPath()
	if err != nil {
		return err
	}
	if err := placeInto(source, target); err != nil {
		return fmt.Errorf("magus self place: %w", err)
	}
	fmt.Printf("Placed %s (built %s) at %s\n", repl.Commit, repl.BuildDate, target)
	return nil
}

func readPlaceStamp(ctx context.Context, source string) (placeStamp, error) {
	var stamp placeStamp
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

// checkReplacement refuses a replacement that is unstamped or not strictly newer
// than running. An unstamped running build takes any stamped replacement.
func checkReplacement(running types.MagusBuild, repl placeStamp) error {
	built, err := time.Parse(time.RFC3339, repl.BuildDate)
	switch {
	case repl.Dirty:
		return fmt.Errorf("magus self place: the replacement was built from a modified tree, so it carries no stamp")
	case repl.Commit == "" || err != nil:
		return fmt.Errorf("magus self place: the replacement reports no commit or build date, so it carries no stamp")
	}
	current, err := time.Parse(time.RFC3339, running.Date)
	if running.Commit == "" || err != nil {
		return nil
	}
	if !built.After(current) {
		return fmt.Errorf("magus self place: %s (built %s) is not newer than the running %s (built %s)",
			repl.Commit, repl.BuildDate, running.Commit, running.Date)
	}
	return nil
}

func placeInto(source, target string) error {
	body, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return file.WriteFileAtomic(target, body, 0o755)
}

func selfPlaceUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus self place <binary>")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Replace the running magus binary with <binary>, which must report a clean")
	fmt.Fprintln(os.Stderr, "commit stamp newer than the running build. The file is renamed into place,")
	fmt.Fprintln(os.Stderr, "never overwritten, so macOS does not kill the next exec.")
}
