package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/libs/cmdline"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// gateCmd implements `magus gate`, a person's decisions about the local gate.
func gateCmd(ctx context.Context, root string, args []string) error {
	if len(args) == 0 {
		return exit.Usagef("magus gate: requires a subcommand (waive)")
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprintln(os.Stderr, "Usage: "+gateCommandSpec().Usage)
		return nil
	case "waive":
		return gateWaive(ctx, root, args[1:])
	default:
		return exit.Usagef("magus gate: unknown subcommand %q (want waive)", args[0])
	}
}

// gateCommandSpec is `magus gate` as the command registry declares it.
func gateCommandSpec() cmdline.Command {
	i := slices.IndexFunc(commands.Root.Children, func(c cmdline.Command) bool { return c.Name == "gate" })
	return commands.Root.Children[i]
}

// gateWaive is `magus gate waive`, a person's waiver of the push gate for one commit.
func gateWaive(ctx context.Context, root string, args []string) error {
	var reason string
	var revoke bool
	rest, err := cmdParse("gate waive", args, func(fs *flag.FlagSet) {
		fs.StringVar(&reason, "reason", "", "")
		fs.BoolVar(&revoke, "revoke", false, "")
		fs.Usage = func() {
			spec := gateCommandSpec()
			child := spec.Children[slices.IndexFunc(spec.Children, func(c cmdline.Command) bool { return c.Name == "waive" })]
			fmt.Fprintln(os.Stderr, "Usage: "+child.Usage)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, cmp.Or(child.Description, child.Short))
		}
	})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return exit.Usagef("magus gate waive: requires one commit")
	}
	if revoke == (strings.TrimSpace(reason) != "") {
		return exit.Usagef("magus gate waive: takes --reason to waive or --revoke to remove, and not both")
	}
	dir := resolveRootOrEmpty(root)
	if dir == "" {
		return fmt.Errorf("magus gate waive: no workspace here, so there is no repository to name %s in", rest[0])
	}
	commit, err := fullCommit(ctx, dir, rest[0])
	if err != nil {
		return fmt.Errorf("magus gate waive: %w", err)
	}
	store, err := openJobs(ctx, dir, jobOpen{})
	if err != nil {
		return err
	}
	if revoke {
		if _, err := guard.RevokeGateWaiver(ctx, store, commit); err != nil {
			return fmt.Errorf("magus gate waive: %w", err)
		}
		fmt.Printf("revoked the waiver of %s: a push of it meets the gate again\n", commit)
		return nil
	}
	w, err := guard.WaiveGate(ctx, store, commit, reason)
	if err != nil {
		return fmt.Errorf("magus gate waive: %w", err)
	}
	fmt.Printf("waived the gate for %s at %s: a push or `gh pr create` of it passes, and of no other commit\n",
		w.Commit, time.Unix(w.At, 0).Format(time.RFC3339))
	return nil
}

// fullCommit is the full id of the commit rev names in the checkout at dir, through the
// VCS layer the push gate reads revisions with.
func fullCommit(ctx context.Context, dir, rev string) (string, error) {
	res, err := vcs.Resolve(ctx, dir, "", types.VCSOptions{})
	if err != nil {
		return "", err
	}
	if res.VCS == nil {
		return "", fmt.Errorf("no version control at %s to name %s in", dir, rev)
	}
	c, err := res.VCS.FindCommit(ctx, dir, rev)
	if err != nil {
		return "", fmt.Errorf("%s names no commit: %w", rev, err)
	}
	return c.ID, nil
}
