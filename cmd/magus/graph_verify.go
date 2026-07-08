package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

// graphVerify checks the knowledge graph's derived artifacts for drift against the
// running binary. Today it verifies the installed agent skill: after a magus
// upgrade that bumps the skill or schema version, an installed .claude/skills/magus
// copy can fall behind, and nothing else notices. Run in CI (`--strict` makes drift
// a non-zero exit) to catch a stale install before it misleads an agent. Other
// derived artifacts (the committed MAGUS.md routing table, immutable-cache shards)
// are candidates for this same verb later.
func graphVerify(_ context.Context, root string, args []string) error {
	fset := flag.NewFlagSet("graph verify", flag.ContinueOnError)
	strict := fset.Bool("strict", false, "exit non-zero when any drift is found (CI guard)")
	dir := fset.String("dir", root, "repo directory to check")
	fset.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus graph verify [--strict] [--dir <path>]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Check derived knowledge-graph artifacts for drift against this binary.")
		fmt.Fprintln(os.Stderr, "Currently: the installed agent skill (.claude/skills/magus). With --strict,")
		fmt.Fprintln(os.Stderr, "any drift is a non-zero exit for CI.")
	}
	if err := fset.Parse(args); err != nil {
		return err
	}

	status := checkSkillStatus(*dir)
	if status.Stale {
		fmt.Printf("agent skill: STALE - %s\n", status.Detail)
	} else {
		fmt.Printf("agent skill: %s\n", status.Detail)
	}

	if *strict && status.Stale {
		return errSilent{exitCode: 1}
	}
	return nil
}
