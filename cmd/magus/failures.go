package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/interactive/failures"
	"github.com/egladman/magus/internal/log/attr"
)

// promptFailures offers the run's pinned failures for rerun or inspection; see
// failures.Prompt.
func promptFailures(ctx context.Context, root string, h *cache.PrettyHandler) error {
	return failures.Prompt(ctx, h, failures.Actions{
		Rerun:  func(ctx context.Context, f cache.Failure) error { return rerunStepped(ctx, root, f) },
		Output: func(ctx context.Context, f cache.Failure) error { return showOutput(ctx, root, f) },
	})
}

// rerunStepped reruns one failed target with stepping on, through the same
// entry point `magus run <target> <project> --step` uses.
//
// In-process rather than by forking a magus: a subprocess would need the
// terminal handed to it and its exit status interpreted, and the flags are
// already parsed by the function being called.
func rerunStepped(ctx context.Context, root string, f cache.Failure) error {
	args := []string{f.Target}
	if f.Project != "" {
		args = append(args, f.Project)
	}
	args = append(args, "--step")
	slog.InfoContext(ctx, fmt.Sprintf("\n-> magus run %s --step", strings.Join(args[:len(args)-1], " ")), attr.Notice(""))
	return runTarget(ctx, root, runConfig{}, args)
}

// showOutput prints a failure's captured output into the SCROLLING transcript,
// rather than opening a view of it.
//
// That is the whole point. A pager or a pane is a place a reader can get stuck
// in and have to find their way out of; printed output is just output, and
// going back is scrolling, which needs no affordance and cannot trap anyone.
// The pinned band stays where it was the entire time, so the run's failures are
// still on screen underneath it.
func showOutput(ctx context.Context, root string, f cache.Failure) error {
	if f.OutputRef == "" {
		return fmt.Errorf("no captured output for %s (the run predates output capture, or none was recorded), rerun it to capture one", f.Target)
	}
	return queryOutputRef(ctx, root, f.OutputRef, outputRefOpts{})
}
