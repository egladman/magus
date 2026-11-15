package main

import (
	"context"
	"fmt"
	"os"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// jobClaimEnv is what applying a claim record asks of the command line.
func jobClaimEnv() job.ClaimEnv {
	return job.ClaimEnv{
		Config:           globalCfg,
		OpenWorkspace:    openWorkspaceForRead,
		Workspace:        jobWorkspace,
		RefsGraph:        loadRefsGraph,
		PickSymbol:       pickSymbol,
		RenameBlindSpots: renameBlindSpots,
		SymbolCoverage:   symbolCoverage,
	}
}

// applyClaim applies the one claim record records holds and prints the proofs.
func applyClaim(ctx context.Context, root string, records []types.Declaration) error {
	open := func() (*job.Store, error) { return openJobs(ctx, root, jobOpen{DryRun: globalCfg.DryRun}) }
	stored, added, err := job.ApplyClaim(ctx, root, records, open, globalCfg.DryRun, jobClaimEnv())
	if err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		return emitNames(os.Stdout, []string{stored.ID})
	case outputText:
		verb := "claims"
		if globalCfg.DryRun {
			verb = "would claim"
		}
		for _, w := range stored.Claims[len(stored.Claims)-added:] {
			fmt.Printf("%s %s: + %s (%s): %s\n", verb, stored.ID, w.Path, w.Evidence, w.Proof)
		}
		return nil
	default:
		return emitFormatted(opts, stored)
	}
}
