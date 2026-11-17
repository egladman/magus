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
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/workspace"
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
	if err := fset.Parse(commands.ReorderFlagsFirst(fset, args)); err != nil {
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
