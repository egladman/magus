package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"os"
	"slices"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/cmdline"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/types"
)

// jobPinOrAdvance is `magus job pin` and `magus job advance`, which verb says.
func jobPinOrAdvance(ctx context.Context, root, verb string, args []string) error {
	spec := jobCommandSpec()
	child := spec.Children[slices.IndexFunc(spec.Children, func(c cmdline.Command) bool { return c.Name == verb })]
	ids, err := commandParse("job "+verb, args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: "+child.Usage)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, cmp.Or(child.Description, child.Short))
		}
	})
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return exit.Usagef("magus job %s: requires at least one job (`%s` lists them)", verb, hint.LsJobs)
	}
	store, err := openJobs(ctx, resolveRootOrEmpty(root), jobOpen{})
	if err != nil {
		return err
	}
	var rows []types.Job
	for _, id := range ids {
		var changed []types.Job
		if verb == "pin" {
			changed, err = store.PinBinary(ctx, id)
		} else {
			var row types.Job
			row, err = store.AdvanceAuthority(ctx, id)
			changed = []types.Job{row}
		}
		if err != nil {
			return fmt.Errorf("magus job %s: %w", verb, err)
		}
		rows = append(rows, changed...)
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		return emitNames(os.Stdout, ids)
	case outputText:
		for _, row := range rows {
			if verb == "pin" {
				fmt.Printf("pinned %s for %s: %s\n", row.Worktree.Binary, row.ID, row.Worktree.BinaryDigest)
				continue
			}
			fmt.Printf("advanced %s's authority to %s\n", row.ID, job.ShortRevision(row.Authority))
		}
		return nil
	default:
		return emitFormatted(opts, rows)
	}
}
