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
)

// jobGrant is `magus job grant`, a person's approval lowering one rule for one job.
func jobGrant(ctx context.Context, root string, args []string) error {
	spec := jobCommandSpec()
	child := spec.Children[slices.IndexFunc(spec.Children, func(c cmdline.Command) bool { return c.Name == "grant" })]
	var lasting time.Duration
	var revoke bool
	rest, err := cmdParse("job grant", args, func(fs *flag.FlagSet) {
		fs.DurationVar(&lasting, "for", 0, "")
		fs.BoolVar(&revoke, "revoke", false, "")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: "+child.Usage)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, cmp.Or(child.Description, child.Short))
		}
	})
	if err != nil {
		return err
	}
	if len(rest) != 2 {
		return exit.Usagef("magus job grant: requires a job and a rule")
	}
	id, rule := rest[0], rest[1]
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
		ends = "at " + time.Unix(g.Until, 0).Format(time.RFC3339) + " or when the job ends"
	}
	fmt.Printf("granted %s to %s: the guard advises instead of refusing it, ending %s\n", rule, id, ends)
	return nil
}

// grantableRule refuses a rule the guard does not let a person grant, naming the ones it
// does.
func grantableRule(rule string) error {
	if slices.Contains(guard.GrantableRules(), rule) {
		return nil
	}
	return exit.Usagef("magus job grant: %q is not grantable; grantable rules: %s", rule, strings.Join(guard.GrantableRules(), ", "))
}
