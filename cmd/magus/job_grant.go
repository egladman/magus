package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
)

// jobGrant is `magus job grant`, a person's approval lowering one rule for one job, or
// waiving the push gate for one commit.
func jobGrant(ctx context.Context, root string, args []string) error {
	spec := jobCommandSpec()
	child := spec.Children[slices.IndexFunc(spec.Children, func(c command.Command) bool { return c.Name == "grant" })]
	var lasting time.Duration
	var revoke bool
	var commit, reason string
	rest, err := commandParse("job grant", args, func(fs *flag.FlagSet) {
		fs.DurationVar(&lasting, "for", 0, "")
		fs.BoolVar(&revoke, "revoke", false, "")
		command.NegatedBoolVar(fs, &revoke, "revoke")
		fs.StringVar(&commit, "commit", "", "")
		fs.StringVar(&reason, "reason", "", "")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: "+child.Usage)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, cmp.Or(child.Description, child.Short))
		}
	})
	if err != nil {
		return err
	}
	if commit != "" {
		open := func(ctx context.Context, dir string) (*job.Store, error) { return openJobs(ctx, dir, jobOpen{}) }
		return guard.GrantForCommit(ctx, os.Stdout, resolveRootOrEmpty(root), commit, rest, guard.GrantForCommitOptions{Lasting: lasting, Revoke: revoke, Reason: reason}, open)
	}
	if reason != "" {
		return exit.Usagef("magus job grant: --reason records a commit waiver: use `magus job grant --commit <commit> %s --reason <text>`", guard.PushUnverifiedRule)
	}
	if len(rest) != 2 {
		return exit.Usagef("magus job grant: requires a job and a rule")
	}
	id, rule := rest[0], rest[1]
	if rule == guard.PushUnverifiedRule {
		return exit.Usagef("magus job grant: %s waives the push gate for one commit, not a job: use `magus job grant --commit <commit> %s --reason <text>`", guard.PushUnverifiedRule, guard.PushUnverifiedRule)
	}
	if err := guard.GrantableRule(rule); err != nil {
		return err
	}
	if lasting < 0 || revoke && lasting != 0 {
		return exit.Usagef("magus job grant: --for takes a positive duration, and none with --revoke")
	}
	store, err := openJobs(ctx, resolveRootOrEmpty(root), jobOpen{})
	if err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if revoke {
		g, err := store.Revoke(ctx, id, rule)
		if err != nil {
			return fmt.Errorf("magus job grant: %w", err)
		}
		if opts.Format != outputText {
			return emitRecord(opts, g, []string{rule})
		}
		fmt.Printf("revoked %s's grant of %s\n", id, rule)
		return nil
	}
	var emit func(jobtypes.Grant) error
	if opts.Format != outputText {
		emit = func(g jobtypes.Grant) error { return emitRecord(opts, g, []string{rule}) }
	}
	return guard.GrantJobRule(ctx, store, os.Stdout, emit, id, rule, lasting)
}
