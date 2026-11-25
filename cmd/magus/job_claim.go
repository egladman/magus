package main

import (
	"context"
	"fmt"
	"os"

	"github.com/egladman/magus/internal/edit"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/job/evidence"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
)

// jobClaimEnv is what applying a claim record asks of the command line.
func jobClaimEnv() job.ClaimEnv {
	return job.ClaimEnv{
		Config:    globalCfg,
		Workspace: jobWorkspace,
		Callers: func(ctx context.Context, root, name string) ([]jobtypes.ClaimRequest, error) {
			return evidence.ExpandCallers(ctx, root, name, evidence.CallersEnv{
				Config:           globalCfg,
				OpenWorkspace:    openWorkspaceForRead,
				RefsGraph:        loadRefsGraph,
				PickSymbol:       knowledge.PickSymbol,
				RenameBlindSpots: edit.RenameBlindSpots,
				SymbolCoverage:   symbolCoverage,
			})
		},
	}
}

// applyClaim applies the one claim record records holds and prints the proofs.
func applyClaim(ctx context.Context, root string, records []jobtypes.Declaration) error {
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
	case outputJSON, outputJSONL, outputTemplate, outputYAML:
	}
	return emitFormatted(opts, stored)
}
