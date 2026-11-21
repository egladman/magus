package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/types"
)

// agentSkills is the catalog of skills magus ships. Sources, rendering, provenance, installation
// and verification all live in internal/agent now; the CLI owns only presentation.
var agentSkills = agent.Default(types.KnowledgeAPIVersion)

// agentCommand implements `magus agent <subcommand>`: the agent integration commands.
//
// Destinations are explicit arguments, never auto-detected, and writing into a
// repo's agent-config dirs happens only through `install`. AGENTS.md is the one
// file magus refuses to write: `install` prints the block for the developer to
// paste instead.
func agentCommand(ctx context.Context, root string, args []string) error {
	if len(args) == 0 {
		return agentUsageError()
	}
	switch args[0] {
	case "install":
		return agentInstallCommand(ctx, args[1:])
	case "harness":
		return agentHarnessCommand(ctx, root, args[1:])
	case "starter":
		return agentStarterCommand(ctx, root)
	case "adoption":
		return agentAdoptionCommand(args[1:])
	case "-h", "--help", "help":
		agentUsage(os.Stderr)
		return nil
	default:
		return exit.Usagef("magus agent: unknown subcommand %q (want install, harness, starter, or adoption)", args[0])
	}
}

func agentUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus agent <install|harness|starter|adoption> [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Subcommands:")
	termkit.ProseItem(w, termkit.SystemProbe, "  install            ",
		"render the embedded skills and write or stream them into named destinations",
		"(<skills-dir>, ...)")
	termkit.ProseItem(w, termkit.SystemProbe, "  harness            ",
		"install skills for, or verify, a harness wired in the magusfile")
	termkit.ProseItem(w, termkit.SystemProbe, "  starter            ",
		"print a starter AGENTS.md to stdout to own and tweak; never writes a file")
	termkit.ProseItem(w, termkit.SystemProbe, "  adoption           ",
		"report how often agents used the graph versus grep, over shell commands",
		"piped in (stdin or --commands)")
	fmt.Fprintln(w, "")
	termkit.Prose(w, termkit.SystemProbe,
		"magus never writes your AGENTS.md.",
		"That file is yours, and an installer that edits a file you own leaves bytes you did not write and cannot audit.",
		"So `install` PRINTS the managed magus block for you to paste, and only when your AGENTS.md is missing it or carrying a stale one.")
	fmt.Fprintln(w, "")
	termkit.Prose(w, termkit.SystemProbe,
		"Stdout philosophy: `magus agent` is a pure data generator.",
		"To install skills anywhere your shell can reach, use --tar and pipe to tar:")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "  magus agent install --tar | tar -xf - -C <skills-dir>")
	fmt.Fprintln(w, "")
	termkit.Prose(w, termkit.SystemProbe,
		"The write-to-disk form is only for the in-repo, paths-relative-to-<dir> case, where it preserves the previous one-line ergonomics.",
		"Absolute destinations are refused unless --global is set, to keep magus from silently writing outside the working tree.")
	fmt.Fprintln(w, "")
	// Listed here because this Usage replaces the FlagSet's own: without it the
	// flags are reachable but undiscoverable, and an agent told to "defer to -h"
	// would conclude they do not exist.
	fmt.Fprintln(w, "install flags:")
	termkit.ProseItem(w, termkit.SystemProbe, "  --dir <path>   ", "repo directory to install into (default .)")
	termkit.ProseItem(w, termkit.SystemProbe, "  --force        ", "overwrite existing installed skill files")
	termkit.ProseItem(w, termkit.SystemProbe, "  --tar          ", "stream a tar archive to stdout instead of writing files")
	termkit.ProseItem(w, termkit.SystemProbe, "  --global       ", "allow absolute destination paths in write mode")
	termkit.ProseItem(w, termkit.SystemProbe, "  --skill-form   ", "skill form: both (default), short, or full; choose explicitly when one body per skill is wanted")
}

func agentUsageError() error {
	agentUsage(os.Stderr)
	return errors.New("a subcommand is required (try: magus agent install)")
}

// agentInstallCommand renders the embedded skills and either writes them under
// <dir>/<dest>... or streams a tar archive to stdout (--tar). Destinations
// are explicit, never inferred from an agent-host name: magus writes the
// standard format where told and stays out of the host-specific business.
// Absolute destinations are refused unless --global is set; the supported
// way to install skills outside the working tree is
// `magus agent install --tar | tar -xf - -C <absolute path>`.
func agentInstallCommand(ctx context.Context, args []string) error {
	fset := flag.NewFlagSet("agent install", flag.ContinueOnError)
	// The display flags are global, so every command takes them: one gap decays the
	// whole convention. `agent install ... -s` previously died on an undefined flag,
	// and with stderr redirected that looked exactly like a successful install: the
	// skills were never written and nothing said so.
	bindDisplayFlags(fset)
	af := flaggen.BindAgent(fset)
	fset.Usage = func() { agentUsage(os.Stderr) }
	if err := command.Parse(fset, commands.ReorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	form, err := agent.ParseForm(af.SkillForm)
	if err != nil {
		return fmt.Errorf("agent install --skill-form: %w", err)
	}
	dests := fset.Args()
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	if af.Tar {
		return writeSkillTar(opts, dests, form)
	}

	if len(dests) == 0 {
		agentUsage(os.Stderr)
		return fmt.Errorf("agent install: name at least one destination directory (e.g. .claude/skills) or pass --tar")
	}
	if !af.Global {
		for _, d := range dests {
			if filepath.IsAbs(d) || strings.HasPrefix(d, "~") {
				return fmt.Errorf("agent install: destination %q is outside the working tree, pass --global or use `--tar | tar -xf - -C %q` instead", d, d)
			}
		}
	}

	var written, changed, removed []string
	for _, dest := range dests {
		base, leaf := agent.SkillTarget(af.Dir, dest, af.Global)
		w, ch, r, err := agentSkills.InstallSkillTree(base, leaf, af.Force, af.DryRun, form)
		if err != nil {
			return err
		}
		written = append(written, w...)
		changed = append(changed, ch...)
		removed = append(removed, r...)
	}
	if opts.Format != outputText {
		return emitRecord(opts, agent.InstallOutput{DryRun: af.DryRun, Written: agent.NonNil(written), Changed: agent.NonNil(changed), Removed: agent.NonNil(removed)}, written)
	}
	for _, p := range written {
		if af.DryRun {
			slog.With(attr.Component("agent install")).InfoContext(ctx, "would write", slog.String("path", p))
			continue
		}
		slog.With(attr.Component("agent install")).InfoContext(ctx, "wrote", slog.String("path", p))
	}
	agent.ReportRemovedSkills(os.Stdout, removed, af.DryRun)
	printAgentInstallNextSteps(ctx, af.Dir, written, changed, form, af.DryRun)
	return nil
}

func writeSkillTar(opts output.Options, dests []string, form types.SkillForm) error {
	if opts.Format != outputText {
		return exit.Usagef("magus agent install --tar: -o is not supported, stdout is the archive")
	}
	if len(dests) > 1 {
		return fmt.Errorf("agent install --tar: at most one destination path prefix is allowed (the path inside the tar archive)")
	}
	prefix := "."
	if len(dests) == 1 {
		prefix = dests[0]
	}
	body, err := agentSkills.SkillTar(prefix, form)
	if err != nil {
		return err
	}
	if _, err := os.Stdout.Write(body); err != nil {
		return fmt.Errorf("agent install --tar: write stdout: %w", err)
	}
	return nil
}

// printAgentInstallNextSteps prints an actionable hint after install, gated on
// the user-controlled hints preference so MAGUS_HINTS_ENABLED=false silences it.
func printAgentInstallNextSteps(ctx context.Context, dir string, written, changed []string, form types.SkillForm, dryRun bool) {
	if !interactive.HintsEnabled() {
		return
	}
	hints := agentSkills.InstallHints(dir, written, changed, form, dryRun)
	for _, h := range hints {
		interactive.Hint(ctx, h)
	}
	if len(hints) > 0 && !dryRun {
		printAgentsBlockToPaste(ctx, dir)
	}
}

// printAgentsBlockToPaste offers the managed AGENTS.md block for the developer to paste.
func printAgentsBlockToPaste(ctx context.Context, dir string) {
	intro, block, ok := agentSkills.PasteOffer(ctx, dir, declaresRoutingIndex(ctx, dir))
	if !ok {
		return
	}
	interactive.Hint(ctx, intro)
	slog.InfoContext(ctx, "\n"+block, attr.Notice(""))
}

// declaresRoutingIndex asks the opened workspace for its root project's outputs.
func declaresRoutingIndex(ctx context.Context, dir string) bool {
	return agent.DeclaresRoutingIndex(ctx, dir, workspace.FindRoot, func(ctx context.Context, root string) ([]types.Glob, error) {
		ws, err := magus.Inspect(ctx, root, append(cliOptions(), workspace.WithLoadedConfig(globalCfg), magus.WithVersion(version))...)
		if err != nil {
			return nil, err
		}
		p := ws.Get(".")
		if p == nil {
			return nil, nil
		}
		return p.AllOutputs(), nil
	})
}

// agentStarterCommand prints the starter AGENTS.md to stdout, never to a file. The starter is meant
// for the workspace root, so the root override or the working directory decides the routing
// index line.
func agentStarterCommand(ctx context.Context, rootOverride string) error {
	dir := rootOverride
	if dir == "" {
		dir = "."
	}
	fmt.Fprint(os.Stdout, agentSkills.StarterDoc(declaresRoutingIndex(ctx, dir)))
	return nil
}

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
	if err := command.Parse(fset, commands.ReorderFlagsFirst(fset, args)); err != nil {
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
	if err := command.Parse(fset, commands.ReorderFlagsFirst(fset, args)); err != nil {
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
	case outputText:
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
	if err := command.Parse(fset, commands.ReorderFlagsFirst(fset, args)); err != nil {
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
		return nil, ctx, types.NoHarnessProvidersError{}
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

// agentAdoptionCommand implements `magus agent adoption`: read shell commands (stdin or --commands),
// report how often the graph was used versus grep.
func agentAdoptionCommand(args []string) error {
	fset := flag.NewFlagSet("agent adoption", flag.ContinueOnError)
	bindDisplayFlags(fset)
	af := flaggen.BindAgentAdoption(fset)
	fset.Usage = func() { guard.WriteAdoptionUsage(os.Stderr) }
	if err := command.Parse(fset, args); err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	var src io.Reader = os.Stdin
	if af.Commands != "" {
		f, err := os.Open(af.Commands)
		if err != nil {
			return fmt.Errorf("agent adoption: %w", err)
		}
		defer f.Close()
		src = f
	} else if termkit.StdinIsTerminal() {
		guard.WriteAdoptionUsage(os.Stderr)
		return exit.Usagef("magus agent adoption: reads commands from stdin or --commands <file>")
	}

	lines, err := guard.ReadAdoptionCommands(src)
	if err != nil {
		return err
	}
	report := guard.AnalyzeAdoption(lines)

	if opts.Format != outputText {
		names := make([]string, len(report.TopSymbolGreps))
		for i, p := range report.TopSymbolGreps {
			names[i] = p.Pattern
		}
		return emitRecord(opts, report, names)
	}
	guard.WriteAdoptionTable(os.Stdout, report)
	return nil
}
