package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
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
		return usagef("magus agent harness: unknown subcommand %q (want apply, install, or verify)", args[0])
	}
}

func agentHarnessInstallCmd(ctx context.Context, rootOverride string, args []string) error {
	fset := flag.NewFlagSet("agent harness install", flag.ContinueOnError)
	id := fset.String("id", "", "harness descriptor ID; omit to install every magusfile-wired provider")
	bindDisplayFlags(fset)
	fset.Usage = func() { agentHarnessUsage(fset.Output()) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if len(fset.Args()) != 0 {
		return usagef("magus agent harness install: positional arguments are not accepted")
	}
	if lease := proc.LeaseFromContext(ctx); lease != "" {
		return fmt.Errorf("magus agent harness install: bound job %q cannot change a harness skill tree", lease)
	}
	root := resolveRootOrEmpty(rootOverride)
	if root == "" {
		return fmt.Errorf("magus agent harness install: no workspace here: run it from inside one or pass --root <path>")
	}
	ids, ctx, err := resolveHarnessIDs(ctx, rootOverride, *id)
	if err != nil {
		return fmt.Errorf("magus agent harness install: %w", err)
	}
	for _, harnessID := range ids {
		skills, err := agent.HarnessSkillsFor(ctx, root, harnessID)
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
	}
	return nil
}

func agentHarnessApplyCmd(ctx context.Context, rootOverride string, args []string) error {
	fset := flag.NewFlagSet("agent harness apply", flag.ContinueOnError)
	id := fset.String("id", "", "harness descriptor ID; omit to apply every magusfile-wired provider")
	bindDisplayFlags(fset)
	fset.Usage = func() { agentHarnessUsage(fset.Output()) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if len(fset.Args()) != 0 {
		return usagef("magus agent harness apply: positional arguments are not accepted")
	}
	root := resolveRootOrEmpty(rootOverride)
	if root == "" {
		return fmt.Errorf("magus agent harness apply: no workspace here: run it from inside one or pass --root <path>")
	}
	ids, ctx, err := resolveHarnessIDs(ctx, rootOverride, *id)
	if err != nil {
		return fmt.Errorf("magus agent harness apply: %w", err)
	}
	for _, harnessID := range ids {
		update, err := agent.ApplyHarness(ctx, agent.HarnessApplyOptions{
			Root:        root,
			ID:          harnessID,
			DryRun:      globalCfg.DryRun,
			ActingLease: proc.LeaseFromContext(ctx),
		})
		if err != nil {
			return fmt.Errorf("magus agent harness apply: %w", err)
		}
		if err := writeHarnessOutput(os.Stdout, update); err != nil {
			return err
		}
	}
	return nil
}

func agentHarnessVerifyCmd(ctx context.Context, rootOverride string, args []string) error {
	fset := flag.NewFlagSet("agent harness verify", flag.ContinueOnError)
	id := fset.String("id", "", "harness descriptor ID; omit to verify every magusfile-wired provider")
	bindDisplayFlags(fset)
	fset.Usage = func() { agentHarnessUsage(fset.Output()) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if len(fset.Args()) != 0 {
		return usagef("magus agent harness verify: positional arguments are not accepted")
	}
	root := resolveRootOrEmpty(rootOverride)
	if root == "" {
		return fmt.Errorf("magus agent harness verify: no workspace here: run it from inside one or pass --root <path>")
	}
	ids, ctx, err := resolveHarnessIDs(ctx, rootOverride, *id)
	if err != nil {
		return fmt.Errorf("magus agent harness verify: %w", err)
	}
	var firstFail error
	for _, harnessID := range ids {
		result, err := agent.VerifyHarness(ctx, root, harnessID)
		if err != nil {
			return fmt.Errorf("magus agent harness verify: %w", err)
		}
		if err := writeHarnessOutput(os.Stdout, result); err != nil {
			return err
		}
		if result.Status != agent.HarnessVerified && firstFail == nil {
			firstFail = fmt.Errorf("magus agent harness verify: %s (%s)", result.Status, result.Reason)
		}
	}
	return firstFail
}

// resolveHarnessIDs returns one ID when --id is set, otherwise every harness
// the root magusfile wired via magus\harness.provider. Inspect runs first so
// harness spells are registered before LoadHarness looks them up. The returned
// context carries ContextWithWiredHarnesses so a spell wins only when wired.
func resolveHarnessIDs(ctx context.Context, rootOverride, id string) ([]string, context.Context, error) {
	ws, err := inspectWorkspace(ctx, rootOverride)
	if err != nil {
		return nil, ctx, err
	}
	wired := workspaceHarnessNames(ws)
	ctx = agent.ContextWithWiredHarnesses(ctx, wired)
	if id != "" {
		return []string{id}, ctx, nil
	}
	if len(wired) == 0 {
		return nil, ctx, usagef("pass --id <harness-id>, or wire one or more hosts with magus\\harness.provider(<spell>) in the root magusfile")
	}
	return wired, ctx, nil
}

func workspaceHarnessNames(ws types.WorkspaceRepository) []string {
	type harnesses interface{ Harnesses() []string }
	if h, ok := any(ws).(harnesses); ok {
		return h.Harnesses()
	}
	return nil
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
		path := typed.Path
		if path == "" {
			path = "(hooks: none)"
		}
		if _, err = fmt.Fprintf(w, "%s %s harness: %s\n", verb, typed.ID, path); err != nil {
			return err
		}
		if typed.MCPHint != "" {
			_, err = fmt.Fprintf(w, "mcp %s (user-owned; Magus does not write host MCP config):\n%s\n", typed.ID, typed.MCPHint)
		}
	case agent.HarnessVerification:
		_, err = fmt.Fprintf(w, "%s %s harness: %s", typed.Status, typed.ID, typed.Path)
		if typed.Reason != "" {
			_, err = fmt.Fprintf(w, " (%s)", typed.Reason)
		}
		if err == nil {
			_, err = fmt.Fprintln(w)
		}
		if err == nil && typed.MCPStatus != "" {
			_, err = fmt.Fprintf(w, "%s %s mcp guidance: %s", typed.MCPStatus, typed.ID, typed.MCPReason)
			if err == nil {
				_, err = fmt.Fprintln(w)
			}
		}
	default:
		return fmt.Errorf("magus agent harness: unsupported output type %T", value)
	}
	return err
}

func agentHarnessUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus agent harness <apply|install|verify> [--id <harness-id>] [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Apply, install, or verify harnesses selected with magus\\harness.provider(<spell>).")
	fmt.Fprintln(w, "Omit --id to act on every wired provider (several hosts are fine when you bounce")
	fmt.Fprintln(w, "between LLM tools). Or pass --id for one spell / JSON descriptor under harnesses/,")
	fmt.Fprintln(w, ".magus/harnesses/, or $XDG_CONFIG_HOME/magus/harnesses.")
}
