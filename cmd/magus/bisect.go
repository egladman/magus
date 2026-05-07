package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/egladman/magus/internal/ci/flake"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// affectedBisect implements `magus affected --bisect <project> [flags]`.
//
// It reads flake history to confirm a suspected regression exists, finds the
// last known-passing commit from the recorded outcome timestamps, then drives
// the active VCS's bisect to pinpoint the culprit commit. Prints culprit SHA
// + subject + author to stdout; does NOT post PR comments or @mention anyone.
//
// Supported VCSes: git, hg. jj reports ErrUnsupported.
func affectedBisect(ctx context.Context, root string, args []string) error {
	var (
		project *string
		goodSHA *string
		target  *string
	)
	if _, err := cmdParse("affected --bisect", args, func(fs *flag.FlagSet) {
		project = fs.String("bisect", "", "Project to bisect for the regression's culprit commit")
		goodSHA = fs.String("good", "", "Known-good commit SHA to start bisect from (auto-detected from history timestamps when empty)")
		target = fs.String("target", "test", "magus target to bisect (default: test)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus affected --bisect <project> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Drive VCS bisect to find the commit that introduced a test regression.")
			fmt.Fprintln(os.Stderr, "Requires a confirmed regression: clean history followed by consistent")
			fmt.Fprintln(os.Stderr, "failures on an affected project.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags:")
			fs.PrintDefaults()
		}
	}); err != nil {
		return err
	}
	if *project == "" {
		return flag.ErrHelp
	}
	projectPath := *project

	// Load history.
	historyPath := globalCfg.HistoryPath
	var h forecast.History
	if err := h.Load(ctx, historyPath); err != nil {
		return fmt.Errorf("bisect: load history %q: %w", historyPath, err)
	}

	// Verify a regression is actually suspected.
	cfg := flake.Config{
		Enabled:          globalCfg.Flake.Enabled,
		BootstrapSamples: globalCfg.Flake.BootstrapSamples,
		MinSamples:       globalCfg.Flake.MinSamples,
		Threshold:        globalCfg.Flake.Threshold,
	}
	rt := flake.NewRuntime(&h, "", cfg, nil)
	if !rt.IsRegression(projectPath, *target) {
		stats := rt.Stats(projectPath, *target)
		fmt.Fprintf(os.Stderr, "bisect: need ≥%d outcomes, flake score < %.0f%%, and two consecutive affected failures\n",
			cfg.MinSamples, cfg.Threshold*100)
		fmt.Fprintf(os.Stderr, "bisect: outcomes recorded: %d, flake score: %.1f%%\n",
			len(stats.RecentOutcomes), rt.Score(projectPath, *target)*100)
		return fmt.Errorf("bisect: no confirmed regression detected for %q/%q", projectPath, *target)
	}

	// Resolve the active VCS from the workspace root.
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil {
		return fmt.Errorf("bisect: %w", err)
	}

	wsRoot, err := res.VCS.Root(ctx, root)
	if errors.Is(err, types.ErrVCSUnsupported) {
		return fmt.Errorf("bisect: VCS %q does not support bisect (supported: git, hg)", res.Name)
	}
	if err != nil {
		return fmt.Errorf("bisect: find vcs root: %w", err)
	}

	// Build the bisect run command. Disable flake retry so bisect sees raw
	// failures, and scope to the project under test.
	self, err := os.Executable()
	if err != nil {
		self = "magus"
	}
	testCmd := fmt.Sprintf("%s run %s --no-flake-retry %s", self, *target, projectPath)

	// Determine the good commit.
	opts := types.BisectOptions{
		Good:    *goodSHA,
		Bad:     "HEAD",
		TestCmd: testCmd,
	}
	if opts.Good == "" {
		lastPass := rt.LastPassTime(projectPath, *target)
		if lastPass.IsZero() {
			return errors.New("bisect: no passing run found in history; provide --good <sha>")
		}
		opts.GoodBefore = lastPass.Add(time.Minute)
		fmt.Fprintf(os.Stderr, "bisect: deriving good commit from last recorded pass: %s\n",
			lastPass.Format(time.RFC3339))
	}

	fmt.Fprintf(os.Stderr, "bisect: starting %s bisect between HEAD and good commit\n", res.Name)

	culprit, err := res.VCS.Bisect(ctx, wsRoot, opts)
	if errors.Is(err, types.ErrVCSUnsupported) {
		return fmt.Errorf("bisect: VCS %q does not support bisect (supported: git, hg)", res.Name)
	}
	if err != nil {
		return fmt.Errorf("bisect: %w", err)
	}

	fmt.Printf("suspected culprit: %s  %s\n", culprit.SHA, culprit.Info)
	return nil
}
