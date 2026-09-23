package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/trail"
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
	root := resolveRootOrEmpty(rootOverride)
	if root == "" {
		return fmt.Errorf("magus agent harness install: no workspace here: run it from inside one or pass --root <path>")
	}
	if lease := harnessActingLease(ctx, root); lease != "" {
		return fmt.Errorf("magus agent harness install: bound job %q cannot change a harness skill tree", lease)
	}
	ids, ctx, err := resolveHarnessIDs(ctx, rootOverride, *id)
	if err != nil {
		return fmt.Errorf("magus agent harness install: %w", err)
	}
	for _, harnessID := range ids {
		skills, err := agent.LoadHarnessSkills(ctx, root, harnessID)
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

func agentHarnessApplyCmd(ctx context.Context, rootOverride string, args []string) error {
	return runHarnessChange(ctx, rootOverride, args, "apply", func(ctx context.Context, root, id string) (agent.HarnessUpdate, error) {
		return agent.ApplyHarness(ctx, agent.HarnessApplyOptions{
			Root: root, ID: id, DryRun: globalCfg.DryRun, ActingLease: harnessActingLease(ctx, root),
		})
	})
}

// harnessActingLease is the lease a harness change is refused under: the checkout's
// binding, else the claim this process was launched with.
func harnessActingLease(ctx context.Context, root string) string {
	lease, _ := checkoutLease(root, proc.LeaseFromContext(ctx))
	return lease
}

// checkoutLease resolves the lease a CLI call acts under in root's checkout, in
// job.LeaseQuery's order: the checkout-wide binding, else claim. A CLI process knows no
// host session or subagent, so those sources never answer here. A cache dir that does not
// resolve leaves the claim to answer alone.
func checkoutLease(root, claim string) (string, types.LeaseSource) {
	q := job.LeaseQuery{Claim: claim}
	if dir, err := magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg)); err == nil {
		q.CacheDir = dir
	}
	return q.Resolve()
}

// agentHarnessRemoveCmd is ApplyHarness's inverse on the CLI: it deletes only the
// managed entries and config_defaults values apply would have written, and never
// asks for confirmation (see RemoveHarness's doc comment for why). It prints
// exactly what changed the same way apply does, and honors --dry-run to preview a
// removal first.
func agentHarnessRemoveCmd(ctx context.Context, rootOverride string, args []string) error {
	return runHarnessChange(ctx, rootOverride, args, "remove", func(ctx context.Context, root, id string) (agent.HarnessUpdate, error) {
		return agent.RemoveHarness(ctx, agent.HarnessRemoveOptions{
			Root: root, ID: id, DryRun: globalCfg.DryRun, ActingLease: harnessActingLease(ctx, root),
		})
	})
}

// runHarnessChange runs `agent harness <verb> [--id]`: change against one descriptor, or
// every magusfile-wired provider when --id is omitted, printing each update.
func runHarnessChange(ctx context.Context, rootOverride string, args []string, verb string,
	change func(ctx context.Context, root, id string) (agent.HarnessUpdate, error),
) error {
	name := "magus agent harness " + verb
	fset := flag.NewFlagSet("agent harness "+verb, flag.ContinueOnError)
	id := fset.String("id", "", "harness descriptor ID; omit to "+verb+" every magusfile-wired provider")
	bindDisplayFlags(fset)
	fset.Usage = func() { agentHarnessUsage(fset.Output()) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if len(fset.Args()) != 0 {
		return usagef("%s: positional arguments are not accepted", name)
	}
	root := resolveRootOrEmpty(rootOverride)
	if root == "" {
		return fmt.Errorf("%s: no workspace here: run it from inside one or pass --root <path>", name)
	}
	ids, ctx, err := resolveHarnessIDs(ctx, rootOverride, *id)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	for _, harnessID := range ids {
		update, err := change(ctx, root, harnessID)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := writeHarnessOutput(os.Stdout, update); err != nil {
			return err
		}
		recordHarnessChange(ctx, root, verb, update)
	}
	return nil
}

// recordHarnessChange appends a host-config change to the activity trail. The hook wiring
// is what lets the guard see an agent at all, so its arrival and removal belong beside the
// guard_policy rows for the rules it carries. A plan or a no-op records nothing.
func recordHarnessChange(ctx context.Context, root, verb string, update agent.HarnessUpdate) {
	if !update.Changed || update.Planned {
		return
	}
	base, err := magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg))
	if err != nil {
		return
	}
	trail.Append(ctx, base, trail.Event{
		Ts:        time.Now().UnixMilli(),
		Kind:      trail.KindConfigChange,
		Origin:    types.Origin{EntryPoint: types.EntryPointCLI},
		Workspace: root,
		Action:    "harness." + verb,
		Outcome:   trail.OutcomeOK,
		Preview:   update.ID + " " + update.Path,
	})
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
		// Skills-only is not a failure: a descriptor that wires no guard at all (an
		// empty config.path) has nothing to be uncovered, and treating it as one
		// would make every skills-only host block `magus agent harness verify`
		// forever. It must still never print as "verified"; see writeHarnessOutput.
		if result.Status != agent.HarnessVerified && result.Status != agent.HarnessSkillsOnly && firstFail == nil {
			firstFail = fmt.Errorf("magus agent harness verify: %s (%s)", result.Status, result.Reason)
		}
		// A missing approval prompt fails even a skills-only host: the guard then refuses
		// every call that needs the person's approval, and verify is where that is said.
		if result.PromptStatus != "" && result.PromptStatus != agent.HarnessVerified && firstFail == nil {
			firstFail = fmt.Errorf("magus agent harness verify: %s approval prompt for %s (%s)", result.PromptStatus, result.ID, result.PromptReason)
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
		for _, prompt := range typed.Prompts {
			if err == nil {
				_, err = fmt.Fprintf(w, "%s %s approval prompt: %s\n", verb, typed.ID, prompt)
			}
		}
		if err == nil && typed.MCPHint != "" {
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
		if err == nil && typed.PromptStatus != "" {
			_, err = fmt.Fprintf(w, "%s %s approval prompt", typed.PromptStatus, typed.ID)
			if err == nil && typed.PromptReason != "" {
				_, err = fmt.Fprintf(w, " (%s)", typed.PromptReason)
			}
			if err == nil {
				_, err = fmt.Fprintln(w)
			}
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
	fmt.Fprintln(w, "between LLM tools). Or pass --id for one spell.")
}
