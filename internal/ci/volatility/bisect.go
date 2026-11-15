package volatility

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// BisectResult is the culprit commit a bisect found for one project target.
type BisectResult struct {
	Project string `json:"project"`
	Target  string `json:"target"`
	VCS     string `json:"vcs"`
	Culprit string `json:"culprit"`
	Info    string `json:"info"`
}

// Bisect confirms from h that projectPath/target has a suspected regression, then
// drives the active VCS's bisect from root to the commit that introduced it.
//
// It reads history only and never retries. good is the last known-passing
// commit; empty derives it from the last pass recorded in h. Supported VCSes
// are git, hg and sl; jj reports an unsupported error.
func Bisect(ctx context.Context, root string, h *forecast.History, cfg Config, projectPath, target, good string) (BisectResult, error) {
	rt := NewRuntime(h, "", cfg, nil, false)
	if !rt.IsRegression(projectPath, target) {
		stats := rt.Stats(projectPath, target)
		slog.InfoContext(ctx, fmt.Sprintf("need >=%d outcomes, volatility score < %.0f%%, and two consecutive affected failures",
			cfg.MinSamples, cfg.Threshold*100), attr.Notice(""), attr.Component("bisect"))
		slog.InfoContext(ctx, fmt.Sprintf("outcomes recorded: %d, volatility score: %.1f%%",
			len(stats.RecentOutcomes), rt.Score(projectPath, target)*100), attr.Notice(""), attr.Component("bisect"))
		return BisectResult{}, fmt.Errorf("bisect: no confirmed regression detected for %q/%q", projectPath, target)
	}

	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil {
		return BisectResult{}, fmt.Errorf("bisect: %w", err)
	}

	wsRoot, err := res.VCS.Root(ctx, root)
	if errors.Is(err, types.ErrVCSUnsupported) {
		return BisectResult{}, fmt.Errorf("bisect: VCS %q does not support bisect (supported: git, hg, sl)", res.Name)
	}
	if err != nil {
		return BisectResult{}, fmt.Errorf("bisect: find vcs root: %w", err)
	}

	// Volatility retry is off so bisect sees raw failures, and the run is scoped
	// to the project under test.
	self, err := os.Executable()
	if err != nil {
		self = "magus"
	}
	opts := types.BisectOptions{
		Good:    good,
		Bad:     "HEAD",
		TestCmd: fmt.Sprintf("%s run %s --no-volatility-retry %s", self, target, projectPath),
	}
	if opts.Good == "" {
		lastPass := rt.LastPassTime(projectPath, target)
		if lastPass.IsZero() {
			return BisectResult{}, errors.New("bisect: no passing run found in history, provide --good <sha>")
		}
		opts.GoodBefore = lastPass.Add(time.Minute)
		slog.InfoContext(ctx, "deriving good commit from last recorded pass: "+lastPass.Format(time.RFC3339),
			attr.Notice(""), attr.Component("bisect"))
	}

	slog.InfoContext(ctx, fmt.Sprintf("starting %s bisect between HEAD and good commit", res.Name),
		attr.Notice(""), attr.Component("bisect"))

	culprit, err := res.VCS.Bisect(ctx, wsRoot, opts)
	if errors.Is(err, types.ErrVCSUnsupported) {
		return BisectResult{}, fmt.Errorf("bisect: VCS %q does not support bisect (supported: git, hg, sl)", res.Name)
	}
	if err != nil {
		return BisectResult{}, fmt.Errorf("bisect: %w", err)
	}
	return BisectResult{Project: projectPath, Target: target, VCS: res.Name, Culprit: culprit.ID, Info: culprit.Info}, nil
}
