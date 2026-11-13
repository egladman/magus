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

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/types"
)

// agentHarnessCommand is the generic, descriptor-driven harness command. It does
// not know a provider name, host config path, matcher, or response format.
func agentHarnessCommand(ctx context.Context, rootOverride string, args []string) error {
	if len(args) == 0 {
		agentHarnessUsage(os.Stderr)
		return exit.Usagef("magus agent harness: a subcommand is required")
	}
	switch args[0] {
	case "install":
		return agentHarnessInstallCommand(ctx, rootOverride, args[1:])
	case "link":
		return agentHarnessLinksCommand(ctx, rootOverride, args[1:])
	case "verify":
		return agentHarnessVerifyCommand(ctx, rootOverride, args[1:])
	case "-h", "--help", "help":
		agentHarnessUsage(os.Stdout)
		return nil
	default:
		return exit.Usagef("magus agent harness: unknown subcommand %q (want install, link, or verify)", args[0])
	}
}

func agentHarnessInstallCommand(ctx context.Context, rootOverride string, args []string) error {
	fset := flag.NewFlagSet("agent harness install", flag.ContinueOnError)
	id := fset.String("id", "", "harness descriptor ID; omit to install every magusfile-wired provider")
	bindDisplayFlags(fset)
	fset.Usage = func() { agentHarnessUsage(fset.Output()) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if len(fset.Args()) != 0 {
		return exit.Usagef("magus agent harness install: positional arguments are not accepted")
	}
	root := resolveRootOrEmpty(rootOverride)
	if root == "" {
		return fmt.Errorf("magus agent harness install: no workspace here: run it from inside one or pass --root <path>")
	}
	lease, _, err := checkoutLease(ctx, root)
	if err != nil {
		return fmt.Errorf("magus agent harness install: %w", err)
	}
	if lease != "" {
		return fmt.Errorf("magus agent harness install: bound job %q cannot change a harness skill tree", lease)
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	text := opts.Format == outputText
	ids, ctx, err := resolveHarnessIDs(ctx, rootOverride, *id)
	if err != nil {
		return fmt.Errorf("magus agent harness install: %w", err)
	}
	plans := make(map[string]types.HarnessInstallation, len(ids))
	for _, harnessID := range ids {
		plan, err := agent.PlanHarness(ctx, root, harnessID)
		if err != nil {
			return fmt.Errorf("magus agent harness install: %w", err)
		}
		plans[harnessID] = plan
	}
	rec := agentHarnessInstallOutput{DryRun: globalCfg.DryRun, Harnesses: []agentHarnessInstallResult{}}
	for _, harnessID := range ids {
		result, err := installHarness(ctx, root, harnessID, plans[harnessID], text)
		if err != nil {
			return err
		}
		if text {
			printHarnessInstall(result)
		}
		rec.Harnesses = append(rec.Harnesses, result)
	}
	if !globalCfg.DryRun {
		writeGuardSnapshot(ctx, root, rootOverride)
	}
	if !text {
		return emitRecord(opts, rec, ids)
	}
	return nil
}

// writeGuardSnapshot loads the guard's policy with no hook deadline, so the
// hooks that follow judge from its snapshot and warm bytecode. A policy
// that does not load is the hooks' to report; install still succeeds.
func writeGuardSnapshot(ctx context.Context, root, rootOverride string) {
	w := guard.Wiring{Root: root, Config: globalCfg, CacheDir: guardCacheDir(rootOverride)}
	if err := guard.WritePolicySnapshot(ctx, w); err != nil {
		slog.With(attr.Component("agent harness install")).WarnContext(ctx,
			"the guard policy snapshot was not written, so the first hook loads the policy", attr.Error(err))
	}
}

// installHarness writes one harness's skills, tool rows and planned files.
func installHarness(ctx context.Context, root, id string, plan types.HarnessInstallation, text bool) (agentHarnessInstallResult, error) {
	result := agentHarnessInstallResult{ID: id, Skills: []string{}, Removed: []string{}}
	if err := installHarnessSkills(ctx, root, &result, text); err != nil {
		return result, err
	}
	changed, err := agent.InstallHarnessTools(ctx, root, id, globalCfg.DryRun)
	if err != nil {
		return result, fmt.Errorf("magus agent harness install: %w", err)
	}
	result.Tools = nonNil(changed)
	installed, err := agent.ApplyHarnessInstallation(ctx, root, plan, globalCfg.DryRun)
	if err != nil {
		return result, fmt.Errorf("magus agent harness install: %w", err)
	}
	result.Files = nonNil(installed)
	result.RestartRequired = plan.Guard == "plugin" && len(installed) > 0
	if !globalCfg.DryRun {
		paths := slices.Concat(result.Skills, result.Removed, result.Tools, result.Files)
		recordHarnessWrite(ctx, root, "agent harness install", paths)
	}
	return result, nil
}

// recordHarnessWrite files the paths a harness command wrote. A store error
// is logged, since the write itself succeeded.
func recordHarnessWrite(ctx context.Context, root, command string, paths []string) {
	if len(paths) == 0 {
		return
	}
	dir, err := sessions.Dir(root)
	if err == nil {
		err = sessions.RecordHarnessWrite(dir, sessions.HarnessWrite{Paths: paths},
			sessions.InvocationStart{Origin: trail.LocalOrigin(ctx), Workspace: root, Command: command, Version: version})
	}
	if err != nil {
		slog.With(attr.Component(command)).WarnContext(ctx, "harness write not recorded", attr.Error(err))
	}
}

func installHarnessSkills(ctx context.Context, root string, result *agentHarnessInstallResult, text bool) error {
	skills, err := agent.LoadHarnessSkills(ctx, root, result.ID)
	if err != nil {
		return fmt.Errorf("magus agent harness install: %w", err)
	}
	for _, path := range skills.Paths {
		written, removed, err := installHarnessSkillPath(ctx, root, path, skills.Form, globalCfg.DryRun, text)
		if err != nil {
			return err
		}
		result.Skills = append(result.Skills, written...)
		result.Removed = append(result.Removed, removed...)
	}
	return nil
}

func printHarnessInstall(result agentHarnessInstallResult) {
	installVerb, verb := "installed", "wrote"
	if globalCfg.DryRun {
		installVerb, verb = "would install", "would write"
	}
	for _, path := range result.Files {
		fmt.Fprintf(os.Stdout, "%s %s: %s\n", installVerb, result.ID, path)
	}
	if result.RestartRequired {
		fmt.Fprintf(os.Stdout, "restart %s to load the installed runtime\n", result.ID)
	}
	for _, p := range result.Tools {
		fmt.Fprintf(os.Stdout, "%s %s tool rows: %s\n", verb, result.ID, p)
	}
}

// installHarnessSkillPath writes one skill tree and prunes what this binary no
// longer ships, reporting both. It overwrites without asking and prunes without
// asking, so what it writes and what it deletes is the only thing standing
// between a person and a skill that left without being named. Same wording as
// `magus agent install`, which reports the same two lists.
func installHarnessSkillPath(ctx context.Context, root, path string, form types.SkillForm, dryRun, text bool) (written, removed []string, err error) {
	if dryRun {
		planned, err := agentSkills.PlanSkillTree(root, path, form)
		if err != nil {
			return nil, nil, err
		}
		stale, err := agentSkills.StaleSkillDirs(root, path, form)
		if err != nil {
			return nil, nil, err
		}
		if text {
			for _, file := range planned {
				fmt.Fprintln(os.Stdout, file)
			}
			reportRemovedSkills(os.Stdout, stale, true)
		}
		return planned, stale, nil
	}
	written, changed, err := agentSkills.WriteSkillTree(root, path, true, form)
	if err != nil {
		return nil, nil, err
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
			slog.With(attr.Component("agent harness install")).DebugContext(ctx, "already current", slog.String("path", file))
			continue
		}
		slog.With(attr.Component("agent harness install")).InfoContext(ctx, "wrote", slog.String("path", file))
	}
	removed, err = agentSkills.PruneSkillTree(root, path, form)
	if err != nil {
		return nil, nil, err
	}
	if text {
		reportRemovedSkills(os.Stdout, removed, false)
	}
	return written, removed, nil
}

type agentHarnessInstallOutput struct {
	DryRun    bool                        `json:"dry_run"`
	Harnesses []agentHarnessInstallResult `json:"harnesses"`
}

type agentHarnessInstallResult struct {
	ID              string   `json:"id"`
	Skills          []string `json:"skills"`
	Removed         []string `json:"removed"`
	Tools           []string `json:"tools"`
	Files           []string `json:"files"`
	RestartRequired bool     `json:"restart_required"`
}

const harnessLinksRequestLimit = 1 << 20

type harnessLinksRequest struct {
	Links map[string]string `json:"links"`
}

type harnessLinksOutput struct {
	Files []string `json:"files"`
}

func agentHarnessLinksCommand(ctx context.Context, rootOverride string, args []string) error {
	fset := flag.NewFlagSet("agent harness link", flag.ContinueOnError)
	stdin := fset.Bool("stdin", false, "read destination-to-source links as JSON from stdin")
	bindDisplayFlags(fset)
	fset.Usage = func() { agentHarnessUsage(fset.Output()) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if len(fset.Args()) != 0 {
		return exit.Usagef("magus agent harness link: positional arguments are not accepted")
	}
	if !*stdin {
		return exit.Usagef("magus agent harness link: --stdin is required")
	}
	root := resolveRootOrEmpty(rootOverride)
	if root == "" {
		return fmt.Errorf("magus agent harness link: no workspace here: run it from inside one or pass --root <path>")
	}
	lease, _, err := checkoutLease(ctx, root)
	if err != nil {
		return fmt.Errorf("magus agent harness link: %w", err)
	}
	if lease != "" {
		return fmt.Errorf("magus agent harness link: bound job %q cannot link workspace sources", lease)
	}
	links, err := readHarnessLinksRequest(os.Stdin)
	if err != nil {
		return fmt.Errorf("magus agent harness link: %w", err)
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	files, err := agent.InstallHarnessSourceLinks(ctx, root, links, globalCfg.DryRun)
	if err != nil {
		return fmt.Errorf("magus agent harness link: %w", err)
	}
	if !globalCfg.DryRun {
		recordHarnessWrite(ctx, root, "agent harness link", files)
	}
	if opts.Format != outputText {
		return emitRecord(opts, harnessLinksOutput{Files: files}, files)
	}
	verb := "linked"
	if globalCfg.DryRun {
		verb = "would link"
	}
	for _, path := range files {
		fmt.Fprintf(os.Stdout, "%s %s\n", verb, path)
	}
	return nil
}

func readHarnessLinksRequest(r io.Reader) (map[string]string, error) {
	data, err := io.ReadAll(io.LimitReader(r, harnessLinksRequestLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	if len(data) > harnessLinksRequestLimit {
		return nil, fmt.Errorf("request exceeds %d bytes", harnessLinksRequestLimit)
	}
	var request harnessLinksRequest
	if err := json.UnmarshalStrict(data, &request); err != nil {
		return nil, fmt.Errorf("decode request: %w", err)
	}
	if request.Links == nil {
		return nil, fmt.Errorf("request must contain a links object")
	}
	return request.Links, nil
}

// checkoutLease resolves the lease a CLI call acts under in root's checkout through
// job.RunLease: the checkout's record, else the claim of the process that asked for the
// work (a forwarded call's lease, else BAGGAGE). A CLI process knows no host session or
// subagent, so those sources never answer here.
func checkoutLease(ctx context.Context, root string) (string, types.LeaseSource, error) {
	loc, err := jobPlace(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve the checkout's lease: %w", err)
	}
	lease, from := job.RunLease(ctx, loc)
	return lease, from, nil
}

// describeHarness renders `magus describe harness [<id>]`: what each wired host's files
// need, and the one command a person runs to merge it. It reads and prints; the host files
// are the person's, so nothing here writes one.
func describeHarness(ctx context.Context, rootOverride string, args []string) error {
	pos, err := commandParse("describe harness", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe harness [<id>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Print the host config a harness wired with magus\\harness.provider(<spell>) needs:")
			fmt.Fprintln(os.Stderr, "each entry a host file lacks, and the one command that merges them. magus never")
			fmt.Fprintln(os.Stderr, "writes host config; run the command yourself. -o json prints the exact fragments")
			fmt.Fprintln(os.Stderr, "that command reads back. Omit <id> for every wired harness.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return exit.Usagef("magus describe harness: takes at most one harness id")
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
	plans := make([]types.HarnessInstallation, 0, len(ids))
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
		return emitItemNames(os.Stdout, plans, func(p types.HarnessInstallation) string { return p.ID })
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

func writeHarnessPlan(w io.Writer, plan types.HarnessInstallation) error {
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
				if change.Op == types.HarnessWrite {
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
		printf("apply the installation:\n  %s\n", plan.Merge)
	}
	for _, wired := range plan.Wired {
		printf("wired in %s %s:\n", wired.File, wired.Key)
		for _, entry := range wired.Entries {
			matcher, commands := agent.EntryCommands(entry)
			if matcher == "" {
				matcher = "any"
			}
			for _, command := range commands {
				printf("  %s: %s\n", matcher, command)
			}
		}
	}
	if plan.MCPHint != "" {
		printf("mcp %s (user-owned; Magus does not write host MCP config):\n%s\n", plan.ID, plan.MCPHint)
	}
	for _, h := range plan.AgentHints {
		printf("agents %s (user-owned model choice): %s\n", plan.ID, h)
	}
	return err
}

func agentHarnessVerifyCommand(ctx context.Context, rootOverride string, args []string) error {
	fset := flag.NewFlagSet("agent harness verify", flag.ContinueOnError)
	id := fset.String("id", "", "harness descriptor ID; omit to verify every magusfile-wired provider")
	bindDisplayFlags(fset)
	fset.Usage = func() { agentHarnessUsage(fset.Output()) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if len(fset.Args()) != 0 {
		return exit.Usagef("magus agent harness verify: positional arguments are not accepted")
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
		if result.Prompt.Failing() && firstFail == nil {
			firstFail = fmt.Errorf("magus agent harness verify: %s approval prompt for %s (%s)", result.Prompt.Status, result.ID, result.Prompt.Reason)
		}
		if result.Setting.Failing() && firstFail == nil {
			firstFail = fmt.Errorf("magus agent harness verify: %s settings for %s (%s)", result.Setting.Status, result.ID, result.Setting.Reason)
		}
		if result.Plugin.Failing() && firstFail == nil {
			firstFail = fmt.Errorf("magus agent harness verify: %s plugin for %s (%s)", result.Plugin.Status, result.ID, result.Plugin.Reason)
		}
		if result.Hooks.Failing() && firstFail == nil {
			firstFail = fmt.Errorf("magus agent harness verify: %s hooks for %s (%s)", result.Hooks.Status, result.ID, result.Hooks.Reason)
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
	wired, err := workspace.WiredHarnesses(ws)
	if err != nil {
		return nil, ctx, err
	}
	ctx = agent.ContextWithWiredHarnesses(ctx, wired)
	// A harness's native prompts name the workspace's human_only targets.
	ctx = types.WithWorkspace(ctx, ws)
	if id != "" {
		return []string{id}, ctx, nil
	}
	if len(wired) == 0 {
		return nil, ctx, exit.Usagef("name a harness id, or wire one or more hosts with magus\\harness.provider(<spell>) in the root magusfile")
	}
	return wired, ctx, nil
}

func writeHarnessVerification(w io.Writer, typed agent.HarnessVerification) error {
	opts, err := output.Parse(global.output)
	if err != nil {
		return err
	}
	if opts.Format == outputName {
		return emitNames(w, []string{typed.ID})
	}
	if opts.Format != outputText {
		return writeFormatted(w, opts, typed)
	}
	_, err = fmt.Fprintf(w, "%s %s harness: %s", typed.Status, typed.ID, typed.Path)
	if err == nil && typed.Reason != "" {
		_, err = fmt.Fprintf(w, " (%s)", typed.Reason)
	}
	if err == nil {
		_, err = fmt.Fprintln(w)
	}
	if err == nil && typed.Prompt.Status != "" {
		_, err = fmt.Fprintf(w, "%s %s approval prompt", typed.Prompt.Status, typed.ID)
		if err == nil && typed.Prompt.Reason != "" {
			_, err = fmt.Fprintf(w, " (%s)", typed.Prompt.Reason)
		}
		if err == nil {
			_, err = fmt.Fprintln(w)
		}
	}
	if err == nil && typed.Setting.Status != "" {
		_, err = fmt.Fprintf(w, "%s %s settings", typed.Setting.Status, typed.ID)
		if err == nil && typed.Setting.Reason != "" {
			_, err = fmt.Fprintf(w, " (%s)", typed.Setting.Reason)
		}
		if err == nil {
			_, err = fmt.Fprintln(w)
		}
	}
	if err == nil && typed.Plugin.Status != "" {
		_, err = fmt.Fprintf(w, "%s %s plugin", typed.Plugin.Status, typed.ID)
		if err == nil && typed.Plugin.Reason != "" {
			_, err = fmt.Fprintf(w, " (%s)", typed.Plugin.Reason)
		}
		if err == nil {
			_, err = fmt.Fprintln(w)
		}
	}
	if err == nil && typed.Hooks.Status != "" {
		_, err = fmt.Fprintf(w, "%s %s hooks", typed.Hooks.Status, typed.ID)
		if err == nil && typed.Hooks.Reason != "" {
			_, err = fmt.Fprintf(w, " (%s)", typed.Hooks.Reason)
		}
		if err == nil {
			_, err = fmt.Fprintln(w)
		}
	}
	if err == nil && typed.MCP.Status != "" {
		_, err = fmt.Fprintf(w, "%s %s mcp guidance: %s", typed.MCP.Status, typed.ID, typed.MCP.Reason)
		if err == nil {
			_, err = fmt.Fprintln(w)
		}
	}
	return err
}

func agentHarnessUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus agent harness <install|link|verify> [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Install or verify harnesses selected with magus\\harness.provider(<spell>), or link workspace source paths from JSON on stdin.")
	fmt.Fprintln(w, "verify runs the wired guard command against a synthetic event rather than trusting")
	fmt.Fprintln(w, "its presence in the config. `magus describe harness`")
	fmt.Fprintln(w, "prints the same installation that install applies, preserving unrelated settings.")
	fmt.Fprintln(w, "link reads a destination-to-source JSON map from stdin and creates in-workspace symlinks after preflight.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Omit --id to act on every wired provider (several hosts are fine when you bounce")
	fmt.Fprintln(w, "between LLM tools). Or pass --id for one spell.")
}
