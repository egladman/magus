package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"slices"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
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
	case "install":
		return agentHarnessInstallCmd(ctx, rootOverride, args[1:])
	case "verify":
		return agentHarnessVerifyCmd(ctx, rootOverride, args[1:])
	case "-h", "--help", "help":
		agentHarnessUsage(os.Stdout)
		return nil
	default:
		return usagef("magus agent harness: unknown subcommand %q (want install or verify; `magus describe harness` prints the host config to merge)", args[0])
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
	lease, err := harnessActingLease(ctx, root)
	if err != nil {
		return fmt.Errorf("magus agent harness install: %w", err)
	}
	if lease != "" {
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

// harnessActingLease is the lease a harness skill install is refused under: the checkout's
// binding, else the claim this process was launched with.
func harnessActingLease(ctx context.Context, root string) (string, error) {
	lease, _, err := checkoutLease(root, proc.LeaseFromContext(ctx))
	return lease, err
}

// checkoutLease resolves the lease a CLI call acts under in root's checkout through
// job.ActingLease: the checkout's record, else claim. A CLI process knows no host
// session or subagent, so those sources never answer here.
func checkoutLease(root, claim string) (string, types.LeaseSource, error) {
	dir, err := magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg))
	if err != nil {
		return "", "", fmt.Errorf("resolve the checkout's lease: %w", err)
	}
	lease, from := job.ActingLease(dir, claim)
	return lease, from, nil
}

// describeHarness renders `magus describe harness [<id>]`: what each wired host's files
// need, and the one command a person runs to merge it. It reads and prints; the host files
// are the person's, so nothing here writes one.
func describeHarness(ctx context.Context, rootOverride string, args []string) error {
	pos, err := cmdParse("describe harness", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe harness [<id>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Print the host config a harness wired with magus\\harness.provider(<spell>) needs:")
			fmt.Fprintln(os.Stderr, "each entry a host file lacks, and the one command that merges them. magus never")
			fmt.Fprintln(os.Stderr, "writes host config; run the command yourself. -o json prints the exact fragments")
			fmt.Fprintln(os.Stderr, "that command reads back. Omit <id> for every wired harness.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return usagef("magus describe harness: takes at most one harness id")
	}
	root := resolveRootOrEmpty(rootOverride)
	if root == "" {
		return fmt.Errorf("magus describe harness: no workspace here: run it from inside one or pass --root <path>")
	}
	id := ""
	if len(pos) == 1 {
		id = pos[0]
	}
	ids, ctx, err := resolveHarnessIDs(ctx, rootOverride, id)
	if err != nil {
		return fmt.Errorf("magus describe harness: %w", err)
	}
	plans := make([]agent.HarnessPlan, 0, len(ids))
	for _, harnessID := range ids {
		plan, err := agent.PlanHarness(ctx, root, harnessID)
		if err != nil {
			return fmt.Errorf("magus describe harness: %s: %w", harnessID, err)
		}
		plans = append(plans, plan)
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		// One plan prints bare, so the merge command can read `.files` straight off it.
		if id != "" {
			return emitFormatted(opts, plans[0])
		}
		return emitFormatted(opts, plans)
	case outputName:
		return emitNamesOf(plans, func(p agent.HarnessPlan) string { return p.ID })
	}
	for i, plan := range plans {
		if i > 0 {
			fmt.Fprintln(os.Stdout)
		}
		if err := writeHarnessPlan(os.Stdout, plan); err != nil {
			return err
		}
	}
	return nil
}

func writeHarnessPlan(w io.Writer, plan agent.HarnessPlan) error {
	var err error
	printf := func(format string, args ...any) {
		if err == nil {
			_, err = fmt.Fprintf(w, format, args...)
		}
	}
	if plan.Current() {
		printf("%s harness: current\n", plan.ID)
	} else {
		printf("%s harness: %d file(s) to merge\n", plan.ID, len(plan.Files))
		for _, path := range slices.Sorted(maps.Keys(plan.Files)) {
			file := plan.Files[path]
			state := "missing"
			if file.Exists {
				state = "exists"
			}
			printf("  %s (%s)\n", path, state)
			for _, change := range file.Changes {
				if change.Op == agent.HarnessWrite {
					printf("    write the whole file\n")
					continue
				}
				value, encErr := json.Marshal(change.Value)
				if encErr != nil && err == nil {
					err = encErr
				}
				printf("    %s %s: %s\n", change.Op, change.Key, value)
			}
		}
		printf("merge it yourself (magus never writes host config):\n  %s\n", plan.Merge)
	}
	if plan.MCPHint != "" {
		printf("mcp %s (user-owned; Magus does not write host MCP config):\n%s\n", plan.ID, plan.MCPHint)
	}
	return err
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
		if err := writeHarnessVerification(os.Stdout, result); err != nil {
			return err
		}
		// Skills-only is not a failure: a descriptor that wires no guard at all (an
		// empty config.path) has nothing to be uncovered, and treating it as one
		// would make every skills-only host block `magus agent harness verify`
		// forever. It must still never print as "verified"; see writeHarnessVerification.
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
		return nil, ctx, usagef("name a harness id, or wire one or more hosts with magus\\harness.provider(<spell>) in the root magusfile")
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

func writeHarnessVerification(w io.Writer, typed agent.HarnessVerification) error {
	opts, err := ResolveOutput(global.output)
	if err != nil {
		return err
	}
	if opts.Format != FormatText {
		return writeFormatted(w, opts, typed)
	}
	_, err = fmt.Fprintf(w, "%s %s harness: %s", typed.Status, typed.ID, typed.Path)
	if err == nil && typed.Reason != "" {
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
	return err
}

func agentHarnessUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus agent harness <install|verify> [--id <harness-id>] [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Install the skill trees of, or verify, harnesses selected with magus\\harness.provider(<spell>).")
	fmt.Fprintln(w, "verify runs the wired guard command against a synthetic event rather than trusting")
	fmt.Fprintln(w, "its presence in the config. The host config itself is yours: `magus describe harness`")
	fmt.Fprintln(w, "prints what to merge and the command that merges it, and magus never writes it.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Omit --id to act on every wired provider (several hosts are fine when you bounce")
	fmt.Fprintln(w, "between LLM tools). Or pass --id for one spell.")
}
