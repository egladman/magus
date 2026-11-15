package main

import (
	"context"
	"fmt"
	"os"

	rootmagus "github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/sessions/brief"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

// briefMemoryMB is the host-capacity claim session --brief takes while it Inspects
// the workspace. Measured 2026-10-03 on this repository: a nested brief peaked near
// 280MB RSS (~64MB heap inuse). Concurrent Inspect samples in panic reports ran
// higher; the declaration seats one brief without inviting a stack of them onto a
// laptop that already holds a go-build and a VM.
const briefMemoryMB = 512

// sessionBriefCommand implements `magus session --brief`.
func sessionBriefCommand(ctx context.Context, root string) error {
	root = resolveRootOrEmpty(root)
	if root == "" {
		return fmt.Errorf("magus session --brief: no workspace here: the brief is read out of a checkout, so run from inside one or pass --root <path>")
	}
	// Claim the host budget before Inspect: the load is what panics measured stacking
	// across parallel hooks. A full budget refuses (exit 75) so a sessionStart hook
	// keeps env-only rather than outspending the machine next to a go-build.
	release, err := admitSessionBrief(ctx, processBrokerClient(), root)
	if err != nil {
		return err
	}
	defer release()
	// The magusfile is optional here for the reason `session checkpoint` makes it
	// optional: the states where it will not load are exactly the ones where a
	// reader most needs to be told where the work stands.
	var ws types.WorkspaceRepository
	if loaded, err := inspectWorkspace(ctx, root); err == nil {
		ws, root = loaded, loaded.Root()
	}
	payload := brief.Gather(ctx, root, ws, briefReaders())

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputText:
		fmt.Print(payload.Text())
		return nil
	case outputName:
		// The revision, matching `magus session checkpoint -o name`: the one value
		// that identifies the state this brief describes. A tree with no revision to
		// report prints nothing rather than a blank line, which a caller reading
		// names line by line would take for an entry.
		if payload.Revision == "" {
			return nil
		}
		return emitNames(os.Stdout, []string{payload.Revision})
	}
	return emitFormatted(opts, payload)
}

// admitSessionBrief takes the host-capacity claim for the workspace Inspect that
// follows. broker: off skips; best-effort admits unarbitrated when no broker answers;
// required refuses (MGS3022).
func admitSessionBrief(ctx context.Context, admit cache.MachineAdmitter, root string) (func(), error) {
	if globalCfg.Broker.Resolved() == types.BrokerOff {
		return func() {}, nil
	}
	required := globalCfg.Broker.Resolved() == types.BrokerRequired
	return cache.AdmitMachine(ctx, admit, types.MachineClaim{
		Project:  ".",
		Target:   "session-brief",
		MemoryMB: briefMemoryMB,
		Dir:      root,
	}, required, globalCfg.BudgetWait)
}

// briefReaders is how the brief reads the job store and the process's configuration.
func briefReaders() brief.Readers {
	return brief.Readers{
		OpenJobs: func(ctx context.Context, root string) (*job.Store, error) { return openJobs(ctx, root, jobOpen{}) },
		ActingLease: func(ctx context.Context, root string) string {
			lease, _, _ := checkoutLease(ctx, root)
			return lease
		},
		PromptCache: promptCacheForCheckout,
		CacheDir: func(root string) (string, error) {
			return rootmagus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
		},
		ResolveRoot: resolveRootOrEmpty,
		ConsoleURL:  consoleRootURL,
		InCheckout:  invocationsInCheckout,
	}
}

// readJobRows lists the repository's job store once for both the leases and the tree.
func readJobRows(ctx context.Context, root string) ([]types.Job, error) {
	store, err := openJobs(ctx, root, jobOpen{})
	if err != nil {
		return nil, err
	}
	return store.List()
}
