package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/proc"
)

// agentHarnessCmd is the generic, descriptor-driven harness surface. It does
// not know a provider name, host config path, matcher, or response format.
func agentHarnessCmd(ctx context.Context, rootOverride string, args []string) error {
	if len(args) == 0 {
		agentHarnessUsage(os.Stderr)
		return usagef("magus agent harness: a subcommand is required")
	}
	switch args[0] {
	case "apply":
		return agentHarnessApplyCmd(ctx, rootOverride, args[1:])
	case "install":
		return agentHarnessInstallCmd(ctx, rootOverride, args[1:])
	case "verify":
		return agentHarnessVerifyCmd(ctx, rootOverride, args[1:])
	case "-h", "--help", "help":
		agentHarnessUsage(os.Stdout)
		return nil
	default:
		return usagef("magus agent harness: unknown subcommand %q (want apply or verify)", args[0])
	}
}

func agentHarnessInstallCmd(ctx context.Context, rootOverride string, args []string) error {
	fset := flag.NewFlagSet("agent harness install", flag.ContinueOnError)
	host := fset.String("host", "", "harness descriptor ID")
	bindDisplayFlags(fset)
	fset.Usage = func() { agentHarnessUsage(fset.Output()) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if len(fset.Args()) != 0 || *host == "" {
		return usagef("magus agent harness install: --host <harness-id> is required and positional arguments are not accepted")
	}
	if lease := proc.LeaseFromContext(ctx); lease != "" {
		return fmt.Errorf("magus agent harness install: bound job %q cannot change a harness skill tree", lease)
	}
	root := resolveRootOrEmpty(rootOverride)
	if root == "" {
		return fmt.Errorf("magus agent harness install: no workspace here: run it from inside one or pass --root <path>")
	}
	skills, err := agent.HarnessSkillsFor(root, *host)
	if err != nil {
		return fmt.Errorf("magus agent harness install: %w", err)
	}
	for _, path := range skills.Paths {
		if globalCfg.DryRun {
			planned, err := agentSkills.PlanSkillTree(root, path, skills.Form)
			if err != nil {
				return err
			}
			for _, file := range planned {
				fmt.Fprintln(os.Stdout, file)
			}
			continue
		}
		if _, err := agentSkills.WriteSkillTree(root, path, true, skills.Form); err != nil {
			return err
		}
		if _, err := agentSkills.PruneSkillTree(root, path, skills.Form); err != nil {
			return err
		}
	}
	return nil
}

func agentHarnessApplyCmd(ctx context.Context, rootOverride string, args []string) error {
	fset := flag.NewFlagSet("agent harness apply", flag.ContinueOnError)
	host := fset.String("host", "", "harness descriptor ID")
	bindDisplayFlags(fset)
	fset.Usage = func() { agentHarnessUsage(fset.Output()) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if len(fset.Args()) != 0 || *host == "" {
		return usagef("magus agent harness apply: --host <harness-id> is required and positional arguments are not accepted")
	}
	root := resolveRootOrEmpty(rootOverride)
	if root == "" {
		return fmt.Errorf("magus agent harness apply: no workspace here: run it from inside one or pass --root <path>")
	}
	update, err := agent.ApplyHarness(agent.HarnessApplyOptions{
		Root:        root,
		Host:        *host,
		DryRun:      globalCfg.DryRun,
		ActingLease: proc.LeaseFromContext(ctx),
	})
	if err != nil {
		return fmt.Errorf("magus agent harness apply: %w", err)
	}
	return writeHarnessOutput(os.Stdout, update)
}

func agentHarnessVerifyCmd(_ context.Context, rootOverride string, args []string) error {
	fset := flag.NewFlagSet("agent harness verify", flag.ContinueOnError)
	host := fset.String("host", "", "harness descriptor ID")
	bindDisplayFlags(fset)
	fset.Usage = func() { agentHarnessUsage(fset.Output()) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if len(fset.Args()) != 0 || *host == "" {
		return usagef("magus agent harness verify: --host <harness-id> is required and positional arguments are not accepted")
	}
	root := resolveRootOrEmpty(rootOverride)
	if root == "" {
		return fmt.Errorf("magus agent harness verify: no workspace here: run it from inside one or pass --root <path>")
	}
	result, err := agent.VerifyHarness(root, *host)
	if err != nil {
		return fmt.Errorf("magus agent harness verify: %w", err)
	}
	return writeHarnessOutput(os.Stdout, result)
}

func writeHarnessOutput(w io.Writer, value any) error {
	opts, err := ResolveOutput(global.output)
	if err != nil {
		return err
	}
	if opts.Format != FormatText {
		return writeFormatted(w, opts, value)
	}
	switch typed := value.(type) {
	case agent.HarnessUpdate:
		verb := "already current"
		if typed.Planned {
			verb = "would update"
		} else if typed.Changed {
			verb = "updated"
		}
		_, err = fmt.Fprintf(w, "%s %s harness: %s\n", verb, typed.Host, typed.Path)
	case agent.HarnessVerification:
		_, err = fmt.Fprintf(w, "%s %s harness: %s", typed.Status, typed.Host, typed.Path)
		if typed.Reason != "" {
			_, err = fmt.Fprintf(w, " (%s)", typed.Reason)
		}
		if err == nil {
			_, err = fmt.Fprintln(w)
		}
	}
	return err
}

func agentHarnessUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus agent harness <apply|install|verify> --host <harness-id> [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Apply, install, or verify a runtime-loaded user-owned harness descriptor.")
	fmt.Fprintln(w, "Descriptors are discovered from harnesses/, .magus/harnesses/, and $XDG_CONFIG_HOME/magus/harnesses.")
}
