package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

// graphVerify checks the knowledge graph's derived artifacts for drift against the
// running binary. Today it verifies the installed agent skill: after a magus
// upgrade that bumps the skill or schema version, an installed .claude/skills/
// copy can fall behind, and nothing else notices. Run in CI (`--strict` makes drift
// a non-zero exit) to catch a stale install before it misleads an agent. Other
// derived artifacts (the committed MAGUS.md routing table, immutable-cache shards)
// are candidates for this same subcommand later.
func graphVerify(_ context.Context, root string, args []string) error {
	fset := flag.NewFlagSet("graph verify", flag.ContinueOnError)
	// The display flags belong on EVERY command, not only the ones that render a
	// result. A flag that works in most places is worse than one that works
	// nowhere: an agent told to always pass -s hits `flag provided but not
	// defined` here, learns the flag is unreliable, and stops using it - so the
	// whole convention decays from one gap.
	bindDisplayFlags(fset)
	strict := fset.Bool("strict", false, "exit non-zero when any drift is found (CI guard)")
	dir := fset.String("dir", root, "repo directory to check")
	fset.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus graph verify [--strict] [--dir <path>]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Check derived knowledge-graph artifacts for drift against this binary.")
		fmt.Fprintln(os.Stderr, "Currently: the installed agent skills (per platform). With --strict,")
		fmt.Fprintln(os.Stderr, "any drift is a non-zero exit for CI.")
	}
	if err := fset.Parse(args); err != nil {
		return err
	}

	statuses := agentSkills.CheckStatuses(*dir)
	if len(statuses) == 0 {
		fmt.Println("agent skills: not installed (run: magus agent install <dir>, e.g. .claude/skills)")
		return nil
	}
	anyStale := false
	for _, status := range statuses {
		if status.Stale {
			anyStale = true
			fmt.Printf("agent skills (%s): STALE: %s\n", status.Location, status.Detail)
		} else {
			fmt.Printf("agent skills (%s): %s\n", status.Location, status.Detail)
		}
	}

	if *strict && anyStale {
		return errSilent{exitCode: 1}
	}
	return nil
}
