package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	rootmagus "github.com/egladman/magus"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/proc"
)

const agentImproveLimit = 2000

// agentImproveCmd owns command-line parsing and presentation. The review
// policy and provider-harness mutation live in internal/agent so other entry
// points cannot bypass their evidence and lease checks.
func agentImproveCmd(ctx context.Context, rootOverride string, args []string) error {
	fset := flag.NewFlagSet("agent improve", flag.ContinueOnError)
	session := fset.String("session", "", "only evidence from this host session")
	limit := fset.Int("limit", agentImproveLimit, "maximum recent activity events to inspect")
	all := fset.Bool("all", false, "include one-off feedback that has not reached the review threshold")
	apply := fset.Bool("apply", false, "write the selected host's Magus-owned hook entries into this workspace")
	host := fset.String("host", "", "harness descriptor to update with --apply")
	bindDisplayFlags(fset)
	fset.Usage = func() { agentImproveUsage(fset) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if len(fset.Args()) != 0 {
		return usagef("magus agent improve: takes no positional arguments")
	}
	root := resolveRootOrEmpty(rootOverride)
	if root == "" {
		return fmt.Errorf("magus agent improve: no workspace here: run it from inside one or pass --root <path>")
	}
	base, err := rootmagus.ResolveCacheDir(root, rootmagus.WithLoadedConfig(globalCfg))
	if err != nil {
		return fmt.Errorf("magus agent improve: resolve activity store: %w", err)
	}
	report, err := agent.Improve(agent.ImproveOptions{
		Root:        root,
		CacheDir:    base,
		Session:     *session,
		Limit:       *limit,
		IncludeAll:  *all,
		Apply:       *apply,
		Host:        *host,
		DryRun:      globalCfg.DryRun,
		ActingLease: proc.LeaseFromContext(ctx),
	})
	if err != nil {
		return fmt.Errorf("magus agent improve: %w", err)
	}
	opts, err := ResolveOutput(global.output)
	if err != nil {
		return err
	}
	if opts.Format != FormatText {
		return writeFormatted(os.Stdout, opts, report)
	}
	renderAgentImprove(os.Stdout, report)
	return nil
}

func renderAgentImprove(w io.Writer, report agent.ImproveReport) {
	if len(report.Candidates) == 0 {
		fmt.Fprintln(w, "no recurring guard feedback needs review.")
		fmt.Fprintln(w, "one-off denials are corrections in progress; pass --all to inspect them without promoting them.")
	}
	for _, candidate := range report.Candidates {
		followed := "no later magus run request observed"
		if candidate.FollowedSessions > 0 {
			followed = fmt.Sprintf("later magus run requested in %d session(s); execution outcome is unobservable", candidate.FollowedSessions)
		}
		fmt.Fprintf(w, "%s on %s: %d denials across %d session(s); %s\n", candidate.Rule, candidate.Surface, candidate.Denied, candidate.Sessions, followed)
		fmt.Fprintf(w, "  proposed destination: %s (%s confidence)\n", candidate.Destination, candidate.Confidence)
		fmt.Fprintf(w, "  evidence: %s\n", strings.Join(candidate.Evidence, ", "))
		fmt.Fprintf(w, "  next: %s\n", candidate.Next)
	}
	for _, coverage := range report.HarnessCoverage {
		fmt.Fprintf(w, "\n%s %s harness coverage: %s", coverage.Status, coverage.Host, coverage.Path)
		if coverage.Reason != "" {
			fmt.Fprintf(w, " (%s)", coverage.Reason)
		}
		fmt.Fprintln(w)
	}
	for _, update := range report.HarnessUpdates {
		verb := "already current"
		if update.Planned {
			verb = "would update"
		} else if update.Changed {
			verb = "updated"
		}
		fmt.Fprintf(w, "\n%s %s harness: %s\n", verb, update.Host, update.Path)
	}
	if len(report.HarnessUpdates) == 0 {
		fmt.Fprintln(w, "\nAfter a human decision, record it deliberately with `magus memory put` and draft any local rule in magus-local-development. This command made no changes.")
	}
}

func agentImproveUsage(fs *flag.FlagSet) {
	fmt.Fprintln(os.Stderr, "Usage: magus agent improve [--session <id>] [--all] [--apply --host <host>] [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Review recurring guard feedback from the activity trail and propose a deliberate next step.")
	fmt.Fprintln(os.Stderr, "With --apply --host <harness-id>, write only descriptor-declared Magus adapter entries into the local harness.")
	fmt.Fprintln(os.Stderr, "It never writes memory, skills, AGENTS.md, or guard rules.")
	fmt.Fprintln(os.Stderr, "")
	fs.PrintDefaults()
}
