package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/guard"
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
	if err := fset.Parse(commands.ReorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if len(fset.Args()) != 0 {
		return exit.Usagef("magus agent harness install: positional arguments are not accepted")
	}
	root := resolveRootOrEmpty(rootOverride)
	if root == "" {
		return fmt.Errorf("magus agent harness install: no workspace here: run it from inside one or pass --root <path>")
	}
	lease, _, err := guard.CheckoutLease(ctx, root, jobPlace)
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
	rec := agent.HarnessInstallOutput{DryRun: globalCfg.DryRun, Harnesses: []agent.HarnessInstallResult{}}
	installOpts := agent.HarnessInstallOptions{DryRun: globalCfg.DryRun, Text: text, Version: version}
	for _, harnessID := range ids {
		result, err := agentSkills.InstallHarness(ctx, root, harnessID, plans[harnessID], installOpts)
		if err != nil {
			return err
		}
		if text {
			agent.PrintHarnessInstall(os.Stdout, result, globalCfg.DryRun)
		}
		rec.Harnesses = append(rec.Harnesses, result)
	}
	if !globalCfg.DryRun {
		guard.RefreshPolicySnapshot(ctx, guard.Wiring{Root: root, Config: globalCfg, CacheDir: guardCacheDir(rootOverride)})
	}
	if !text {
		return emitRecord(opts, rec, ids)
	}
	return nil
}

func agentHarnessLinksCommand(ctx context.Context, rootOverride string, args []string) error {
	fset := flag.NewFlagSet("agent harness link", flag.ContinueOnError)
	stdin := fset.Bool("stdin", false, "read destination-to-source links as JSON from stdin")
	bindDisplayFlags(fset)
	fset.Usage = func() { agentHarnessUsage(fset.Output()) }
	if err := fset.Parse(commands.ReorderFlagsFirst(fset, args)); err != nil {
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
	lease, _, err := guard.CheckoutLease(ctx, root, jobPlace)
	if err != nil {
		return fmt.Errorf("magus agent harness link: %w", err)
	}
	if lease != "" {
		return fmt.Errorf("magus agent harness link: bound job %q cannot link workspace sources", lease)
	}
	links, err := agent.ReadHarnessLinksRequest(os.Stdin)
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
		agent.RecordHarnessWrite(ctx, root, "agent harness link", version, files)
	}
	if opts.Format != outputText {
		return emitRecord(opts, agent.HarnessLinksOutput{Files: files}, files)
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
		if err := agent.WriteHarnessPlan(os.Stdout, plan); err != nil {
			return err
		}
	}
	return nil
}

func agentHarnessVerifyCommand(ctx context.Context, rootOverride string, args []string) error {
	fset := flag.NewFlagSet("agent harness verify", flag.ContinueOnError)
	id := fset.String("id", "", "harness descriptor ID; omit to verify every magusfile-wired provider")
	bindDisplayFlags(fset)
	fset.Usage = func() { agentHarnessUsage(fset.Output()) }
	if err := fset.Parse(commands.ReorderFlagsFirst(fset, args)); err != nil {
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
		if firstFail == nil {
			firstFail = agent.VerificationFailure(result)
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
	return agent.WriteHarnessVerificationText(w, typed)
}
