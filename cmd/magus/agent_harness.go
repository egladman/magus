package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
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
	case "remove":
		return agentHarnessRemoveCmd(ctx, rootOverride, args[1:])
	case "verify":
		return agentHarnessVerifyCmd(ctx, rootOverride, args[1:])
	case "-h", "--help", "help":
		agentHarnessUsage(os.Stdout)
		return nil
	default:
		return usagef("magus agent harness: unknown subcommand %q (want apply, install, remove, or verify)", args[0])
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
	harnessProblemsFor(ctx, root)
	for _, harnessID := range ids {
		skills, err := agent.HarnessSkillsFor(ctx, root, harnessID)
		if err != nil {
			return fmt.Errorf("magus agent harness install: %w", err)
		}
		for _, path := range skills.Paths {
			if err := installHarnessSkillPath(ctx, root, path, skills.Form, globalCfg.DryRun); err != nil {
				return err
			}
		}
	}
	return nil
}

// installHarnessSkillPath writes one skill tree and prunes what this binary no
// longer ships, reporting both. It overwrites without asking and prunes without
// asking, so what it writes and what it deletes is the only thing standing
// between a person and a skill that left without being named. Same wording as
// `magus agent install`, which reports the same two lists.
func installHarnessSkillPath(ctx context.Context, root, path string, form agent.Form, dryRun bool) error {
	if dryRun {
		planned, err := agentSkills.PlanSkillTree(root, path, form)
		if err != nil {
			return err
		}
		for _, file := range planned {
			fmt.Fprintln(os.Stdout, file)
		}
		stale, err := agentSkills.StaleSkillDirs(root, path, form)
		if err != nil {
			return err
		}
		for _, dir := range stale {
			slog.InfoContext(ctx, "agent harness install: would remove skill this binary no longer ships", slog.String("path", dir))
		}
		return nil
	}
	written, changed, err := agentSkills.WriteSkillTree(root, path, true, form)
	if err != nil {
		return err
	}
	// Logged per file, so the line says whether this install moved that file's bytes.
	// A reinstall that rewrites thirty unchanged files and one real update read the same
	// thirty-one ways before.
	altered := make(map[string]bool, len(changed))
	for _, file := range changed {
		altered[file] = true
	}
	for _, file := range written {
		if !altered[file] {
			slog.DebugContext(ctx, "agent harness install: already current", slog.String("path", file))
			continue
		}
		slog.InfoContext(ctx, "agent harness install: wrote", slog.String("path", file))
	}
	removed, err := agentSkills.PruneSkillTree(root, path, form)
	if err != nil {
		return err
	}
	// Reported at the same level as a write. A silent delete is how a person loses
	// a skill they thought they had.
	for _, file := range removed {
		slog.InfoContext(ctx, "agent harness install: removed skill this binary no longer ships", slog.String("path", file))
	}
	return nil
}

// harnessProblemsFor reports every descriptor that disqualified itself, by name
// and with its reason. Printed on every harness subcommand, not only where it
// changes a verdict: a descriptor magus skipped is a misconfiguration, and a
// skip nobody prints looks exactly like a host nobody installed.
func harnessProblemsFor(ctx context.Context, root string) []agent.HarnessProblem {
	problems, err := agent.HarnessProblems(ctx, root)
	if err != nil {
		slog.ErrorContext(ctx, "agent harness: harness descriptors could not be read", slog.String("error", err.Error()))
		return nil
	}
	for _, p := range problems {
		slog.ErrorContext(ctx, "agent harness: descriptor disqualified itself and was not loaded",
			slog.String("descriptor", p.Source), slog.String("reason", p.Reason))
	}
	return problems
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
	harnessProblemsFor(ctx, root)
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

// agentHarnessRemoveCmd is ApplyHarness's inverse on the CLI: it deletes only the
// managed entries and config_defaults values apply would have written, and never
// asks for confirmation (see RemoveHarness's doc comment for why). It prints
// exactly what changed the same way apply does, through writeHarnessOutput, and
// honors --dry-run to preview a removal first.
func agentHarnessRemoveCmd(ctx context.Context, rootOverride string, args []string) error {
	fset := flag.NewFlagSet("agent harness remove", flag.ContinueOnError)
	id := fset.String("id", "", "harness descriptor ID; omit to remove every magusfile-wired provider")
	bindDisplayFlags(fset)
	fset.Usage = func() { agentHarnessUsage(fset.Output()) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if len(fset.Args()) != 0 {
		return usagef("magus agent harness remove: positional arguments are not accepted")
	}
	root := resolveRootOrEmpty(rootOverride)
	if root == "" {
		return fmt.Errorf("magus agent harness remove: no workspace here: run it from inside one or pass --root <path>")
	}
	ids, ctx, err := resolveHarnessIDs(ctx, rootOverride, *id)
	if err != nil {
		return fmt.Errorf("magus agent harness remove: %w", err)
	}
	harnessProblemsFor(ctx, root)
	for _, harnessID := range ids {
		update, err := agent.RemoveHarness(ctx, agent.HarnessRemoveOptions{
			Root:        root,
			ID:          harnessID,
			DryRun:      globalCfg.DryRun,
			ActingLease: proc.LeaseFromContext(ctx),
		})
		if err != nil {
			return fmt.Errorf("magus agent harness remove: %w", err)
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
	// A descriptor that disqualified itself is a coverage gap on this machine, and
	// verify is the surface whose exit code says so.
	if problems := harnessProblemsFor(ctx, root); len(problems) > 0 {
		firstFail = fmt.Errorf("magus agent harness verify: %d harness descriptor(s) disqualified themselves; the first is %s", len(problems), problems[0])
	}
	for _, harnessID := range ids {
		result, err := agent.VerifyHarness(ctx, root, harnessID)
		if err != nil {
			return fmt.Errorf("magus agent harness verify: %w", err)
		}
		if err := writeHarnessOutput(os.Stdout, result); err != nil {
			return err
		}
		// Skills-only is not a failure: a descriptor that wires no guard at all (an
		// empty config.path) has nothing to be uncovered, and treating it as one
		// would make every skills-only host block `magus agent harness verify`
		// forever. It must still never print as "verified"; see writeHarnessOutput.
		if result.Status != agent.HarnessVerified && result.Status != agent.HarnessSkillsOnly && firstFail == nil {
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
		switch {
		case typed.Removed && typed.Planned:
			verb = "would remove"
		case typed.Removed && typed.Changed:
			verb = "removed"
		case typed.Removed:
			verb = "nothing to remove for"
		case typed.Planned:
			verb = "would update"
		case typed.Changed:
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
	fmt.Fprintln(w, "Usage: magus agent harness <apply|install|remove|verify> [--id <harness-id>] [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Apply, install, remove, or verify harnesses selected with magus\\harness.provider(<spell>).")
	fmt.Fprintln(w, "remove is apply's inverse: it deletes only the managed entries and config_defaults")
	fmt.Fprintln(w, "values apply would have written, and leaves a user's own hooks beside them untouched.")
	fmt.Fprintln(w, "It does not ask for confirmation; pass --dry-run to preview one first.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Omit --id to act on every wired provider (several hosts are fine when you bounce")
	fmt.Fprintln(w, "between LLM tools). Or pass --id for one spell / JSON descriptor under harnesses/,")
	fmt.Fprintln(w, ".magus/harnesses/, or $XDG_CONFIG_HOME/magus/harnesses.")
}
