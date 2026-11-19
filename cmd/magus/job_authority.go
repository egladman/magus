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
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
)

// jobPinOrAdvance is `magus job pin` and `magus job advance`, which verb says.
func jobPinOrAdvance(ctx context.Context, root, verb string, args []string) error {
	spec := jobCommandSpec()
	child := spec.Children[slices.IndexFunc(spec.Children, func(c command.Command) bool { return c.Name == verb })]
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
	rows, err := job.PinOrAdvance(ctx, store, verb, ids)
	if err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		return emitNames(os.Stdout, ids)
	case outputText:
		job.PrintPinned(os.Stdout, verb, rows)
		return nil
	case outputJSON, outputJSONL, outputTemplate, outputYAML:
	}
	return emitFormatted(opts, rows)
}
