package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/ci/volatility"
)

// affectedBisect implements `magus affected --bisect <project> [flags]`.
//
// It reads volatility history to confirm a suspected regression exists, finds the
// last known-passing commit from the recorded outcome timestamps, then drives
// the active VCS's bisect to pinpoint the culprit commit. Prints culprit SHA
// + subject + author to stdout; does NOT post PR comments or @mention anyone.
//
// Supported VCSes: git, hg, sl. jj reports ErrUnsupported.
func affectedBisect(ctx context.Context, root string, args []string) error {
	var bf *flaggen.AffectedBisectFlags
	if _, err := commandParse("affected --bisect", args, func(fs *flag.FlagSet) {
		bf = flaggen.BindAffectedBisect(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus affected --bisect <project> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Drive VCS bisect to find the commit that introduced a test regression.")
			fmt.Fprintln(os.Stderr, "Requires a confirmed regression: clean history followed by consistent")
			fmt.Fprintln(os.Stderr, "failures on an affected project.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags:")
			printOwnDefaults(fs)
		}
	}); err != nil {
		return err
	}
	format, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if bf.Bisect == "" {
		return flag.ErrHelp
	}

	var h forecast.History
	if err := h.Load(ctx, globalCfg.HistoryPath); err != nil {
		return fmt.Errorf("bisect: load history %q: %w", globalCfg.HistoryPath, err)
	}
	cfg := volatility.Config{
		Enabled:          globalCfg.Volatility.Enabled,
		BootstrapSamples: globalCfg.Volatility.BootstrapSamples,
		MinSamples:       globalCfg.Volatility.MinSamples,
		Threshold:        globalCfg.Volatility.Threshold,
	}
	rec, err := volatility.Bisect(ctx, root, &h, cfg, bf.Bisect, bf.Target, bf.Good)
	if err != nil {
		return err
	}
	if format.Format != outputText {
		return emitRecord(format, rec, []string{rec.Culprit})
	}
	fmt.Printf("suspected culprit: %s  %s\n", rec.Culprit, rec.Info)
	return nil
}
