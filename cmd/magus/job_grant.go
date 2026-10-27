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

	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/libs/cmdline"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// pushUngatedRule is the one rule a grant scopes to a commit instead of a job.
const pushUngatedRule = "push-ungated"

// jobGrant is `magus job grant`, a person's approval lowering one rule for one job, or
// waiving the push gate for one commit.
func jobGrant(ctx context.Context, root string, args []string) error {
	spec := jobCommandSpec()
	child := spec.Children[slices.IndexFunc(spec.Children, func(c cmdline.Command) bool { return c.Name == "grant" })]
	var lasting time.Duration
	var revoke bool
	var commit, reason string
	rest, err := commandParse("job grant", args, func(fs *flag.FlagSet) {
		fs.DurationVar(&lasting, "for", 0, "")
		fs.BoolVar(&revoke, "revoke", false, "")
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
		return grantForCommit(ctx, root, commit, rest, grantForCommitOptions{lasting: lasting, revoke: revoke, reason: reason})
	}
	if reason != "" {
		return exit.Usagef("magus job grant: --reason records a commit waiver: use `magus job grant --commit <commit> %s --reason <text>`", pushUngatedRule)
	}
	if len(rest) != 2 {
		return exit.Usagef("magus job grant: requires a job and a rule")
	}
	id, rule := rest[0], rest[1]
	if rule == pushUngatedRule {
		return exit.Usagef("magus job grant: %s waives the push gate for one commit, not a job: use `magus job grant --commit <commit> %s --reason <text>`", pushUngatedRule, pushUngatedRule)
	}
	if err := grantableRule(rule); err != nil {
		return err
	}
	if lasting < 0 || revoke && lasting != 0 {
		return exit.Usagef("magus job grant: --for takes a positive duration, and none with --revoke")
	}
	store, err := openJobs(ctx, resolveRootOrEmpty(root), jobOpen{})
	if err != nil {
		return err
	}
	if revoke {
		if _, err := store.Revoke(ctx, id, rule); err != nil {
			return fmt.Errorf("magus job grant: %w", err)
		}
		fmt.Printf("revoked %s's grant of %s\n", id, rule)
		return nil
	}
	var until int64
	if lasting > 0 {
		until = time.Now().Add(lasting).Unix()
	}
	g, err := store.Grant(ctx, id, rule, until)
	if err != nil {
		return fmt.Errorf("magus job grant: %w", err)
	}
	ends := "when the job ends"
	if g.Until != 0 {
		ends = fmt.Sprintf("at %s or when the job ends", time.Unix(g.Until, 0).Format(time.RFC3339))
	}
	fmt.Printf("granted %s to %s: the guard advises instead of refusing it, ending %s\n", rule, id, ends)
	return nil
}

type grantForCommitOptions struct {
	lasting time.Duration
	revoke  bool
	reason  string
}

// grantForCommit is `magus job grant --commit`, a person's waiver of the push gate for one
// commit, or its revocation.
func grantForCommit(ctx context.Context, root, rev string, rest []string, opts grantForCommitOptions) error {
	if len(rest) != 1 {
		return exit.Usagef("magus job grant: --commit takes one rule, %s", pushUngatedRule)
	}
	if rest[0] != pushUngatedRule {
		if err := grantableRule(rest[0]); err == nil {
			return exit.Usagef("magus job grant: %s is granted to a job, not a commit: use `magus job grant [--for <duration>] <job> %s`", rest[0], rest[0])
		}
		return exit.Usagef("magus job grant: --commit takes %s, not %q", pushUngatedRule, rest[0])
	}
	if opts.lasting != 0 {
		return exit.Usagef("magus job grant: --for does not apply to --commit: a waiver covers its commit until revoked")
	}
	if opts.revoke == (strings.TrimSpace(opts.reason) != "") {
		return exit.Usagef("magus job grant: --commit takes --reason to waive or --revoke to remove, and not both")
	}
	dir := resolveRootOrEmpty(root)
	if dir == "" {
		return fmt.Errorf("magus job grant: no workspace here, so there is no repository to name %s in", rev)
	}
	commit, err := fullCommit(ctx, dir, rev)
	if err != nil {
		return fmt.Errorf("magus job grant: %w", err)
	}
	store, err := openJobs(ctx, dir, jobOpen{})
	if err != nil {
		return err
	}
	if opts.revoke {
		if _, err := guard.RevokeGateWaiver(ctx, store, commit); err != nil {
			return fmt.Errorf("magus job grant: %w", err)
		}
		fmt.Printf("revoked the waiver of %s: a push of it meets the gate again\n", commit)
		return nil
	}
	w, err := guard.WaiveGate(ctx, store, commit, opts.reason)
	if err != nil {
		return fmt.Errorf("magus job grant: %w", err)
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

// grantableRule refuses a rule the guard does not let a person grant, naming the ones it
// does.
func grantableRule(rule string) error {
	if slices.Contains(guard.GrantableRules(), rule) {
		return nil
	}
	return exit.Usagef("magus job grant: %q is not grantable; grantable rules: %s", rule, strings.Join(guard.GrantableRules(), ", "))
}
