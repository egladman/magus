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
	var strict bool
	dir := root
	fs := flag.NewFlagSet("graph verify", flag.ContinueOnError)
	fs.BoolVar(&strict, "strict", false, "exit non-zero when any drift is found (CI guard)")
	fs.StringVar(&dir, "dir", root, "repo directory to check")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus graph verify [--strict] [--dir <path>]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Check derived knowledge-graph artifacts for drift against this binary.")
		fmt.Fprintln(os.Stderr, "Currently: the installed agent skill (.claude/skills/magus). With --strict,")
		fmt.Fprintln(os.Stderr, "any drift is a non-zero exit for CI.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	drift := checkSkillDrift(dir)
	switch {
	case !drift.Installed:
		fmt.Printf("agent skill: %s\n", drift.Detail)
	case drift.Stale:
		fmt.Printf("agent skill: STALE - %s\n", drift.Detail)
	default:
		fmt.Printf("agent skill: %s\n", drift.Detail)
	}

	if strict && drift.Stale {
		return errSilent{exitCode: 1}
	}
	return nil
}
