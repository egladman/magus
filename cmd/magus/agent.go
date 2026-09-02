package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/types"
)

// agentSkills is the catalog of skills magus ships. Sources, rendering, provenance, installation
// and verification all live in internal/agent now; the CLI owns only presentation.
var agentSkills = agent.Default(types.KnowledgeSchemaVersion)

// agentCmd implements `magus agent <subcommand>`: the agent-integration surface.
//
// Destinations are explicit arguments, never auto-detected, and writing into a
// repo's agent-config dirs happens only through `install`. AGENTS.md is the one
// file magus refuses to write - `install` prints the block for the developer to
// paste instead.
func agentCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return agentUsageErr()
	}
	switch args[0] {
	case "install":
		return agentInstallCmd(ctx, args[1:])
	case "sample":
		return agentSampleCmd()
	case "adoption":
		return agentAdoptionCmd(args[1:])
	case "-h", "--help", "help":
		agentUsage(os.Stderr)
		return nil
	default:
		return usagef("magus agent: unknown subcommand %q (want install, sample, or adoption)", args[0])
	}
}

func agentUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus agent <install|sample|adoption> [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Subcommands:")
	tty.ProseItem(w, tty.SystemProbe, "  install            ",
		"render the embedded skills and write or stream them into named destinations",
		"(.claude/skills, .agents/skills, .opencode/skills, ...)")
	tty.ProseItem(w, tty.SystemProbe, "  sample             ",
		"print a starter AGENTS.md to stdout to own and tweak; never writes a file")
	tty.ProseItem(w, tty.SystemProbe, "  adoption           ",
		"report how often agents used the graph versus grep, over shell commands",
		"piped in (stdin or --commands)")
	fmt.Fprintln(w, "")
	tty.Prose(w, tty.SystemProbe,
		"magus never writes your AGENTS.md.",
		"That file is yours, and an installer that edits a file you own leaves bytes you did not write and cannot audit.",
		"So `install` PRINTS the managed magus block for you to paste, and only when your AGENTS.md is missing it or carrying a stale one.")
	fmt.Fprintln(w, "")
	tty.Prose(w, tty.SystemProbe,
		"Stdout philosophy: `magus agent` is a pure data generator.",
		"To install skills anywhere your shell can reach, use --tar and pipe to tar:")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "  magus agent install --tar | tar -xf - -C .claude/skills")
	fmt.Fprintln(w, "  magus agent install --tar | tar -xf - -C ~/.config/opencode/skills")
	fmt.Fprintln(w, "")
	tty.Prose(w, tty.SystemProbe,
		"The write-to-disk form is only for the in-repo, paths-relative-to-<dir> case, where it preserves the previous one-line ergonomics.",
		"Absolute destinations are refused unless --global is set, to keep magus from silently writing outside the working tree.")
	fmt.Fprintln(w, "")
	// Listed here because this Usage replaces the FlagSet's own: without it the
	// flags are reachable but undiscoverable, and an agent told to "defer to -h"
	// would conclude they do not exist.
	fmt.Fprintln(w, "install flags:")
	tty.ProseItem(w, tty.SystemProbe, "  --dir <path>   ", "repo directory to install into (default .)")
	tty.ProseItem(w, tty.SystemProbe, "  --force        ", "overwrite existing installed skill files")
	tty.ProseItem(w, tty.SystemProbe, "  --prune        ",
		"also remove installed skills this binary no longer ships; without it they are reported and left in place.",
		"Only skills magus wrote are candidates - a hand-authored one beside them is never touched")
	tty.ProseItem(w, tty.SystemProbe, "  --tar          ", "stream a tar archive to stdout instead of writing files")
	tty.ProseItem(w, tty.SystemProbe, "  --global       ", "allow absolute destination paths in write mode")
}

func agentUsageErr() error {
	agentUsage(os.Stderr)
	return fmt.Errorf("agent: a subcommand is required (try: install)")
}

// agentInstallCmd renders the embedded skills and either writes them under
// <dir>/<dest>... or streams a tar archive to stdout (--tar). Destinations
// are explicit, never inferred from an agent-host name: magus writes the
// standard format where told and stays out of the host-specific business.
// Absolute destinations are refused unless --global is set; the supported
// way to install skills outside the working tree is `magus agent install
// --tar | tar -xf - -C <absolute path>`.
func agentInstallCmd(ctx context.Context, args []string) error {
	fset := flag.NewFlagSet("agent install", flag.ContinueOnError)
	// The display flags are global, so every command takes them - one gap decays the
	// whole convention. `agent install ... -s` previously died on an undefined flag,
	// and with stderr redirected that looked exactly like a successful install: the
	// skills were never written and nothing said so.
	bindDisplayFlags(fset)
	af := gen.BindAgent(fset)
	// Not implied by --force, and not the default. --force overwrites files this
	// command is about to write and can name; --prune deletes directories the caller
	// has not seen, picked by a rule inside a binary they may have just upgraded.
	// Without it, install still SAYS what is stale, so the orphans stay visible
	// rather than becoming invisible again.
	fset.Usage = func() { agentUsage(os.Stderr) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	dests := fset.Args()

	if af.Tar {
		if len(dests) > 1 {
			return fmt.Errorf("agent install --tar: at most one destination path prefix is allowed (the path inside the tar archive)")
		}
		prefix := "."
		if len(dests) == 1 {
			prefix = dests[0]
		}
		body, err := agentSkills.SkillTar(prefix, agent.VariantSimple)
		if err != nil {
			return err
		}
		if _, err := os.Stdout.Write(body); err != nil {
			return fmt.Errorf("agent install --tar: write stdout: %w", err)
		}
		return nil
	}

	if len(dests) == 0 {
		agentUsage(os.Stderr)
		return fmt.Errorf("agent install: name at least one destination directory (e.g. .claude/skills) or pass --tar")
	}
	if !af.Global {
		for _, d := range dests {
			if filepath.IsAbs(d) || strings.HasPrefix(d, "~") {
				return fmt.Errorf("agent install: destination %q is outside the working tree; pass --global, or use --tar | tar -xf - -C %q instead", d, d)
			}
		}
	}

	var written []string
	var removed, stale []string
	for _, dest := range dests {
		base, leaf := installTarget(af.Dir, dest, af.Global)
		// Answers the question --prune is dangerous without: which directories go.
		// Same two lists the real run reports, nothing touched.
		if af.DryRun {
			w, err := agentSkills.PlanSkillTree(base, leaf, agent.VariantSimple)
			if err != nil {
				return err
			}
			written = append(written, w...)
			s, err := agentSkills.StaleSkillDirs(base, leaf)
			if err != nil {
				return err
			}
			if af.Prune {
				removed = append(removed, s...)
			} else {
				stale = append(stale, s...)
			}
			continue
		}
		w, err := agentSkills.WriteSkillTree(base, leaf, af.Force, agent.VariantSimple)
		if err != nil {
			return err
		}
		written = append(written, w...)
		// After writing, never before: a prune that ran first would delete a skill
		// this install then failed to replace.
		if af.Prune {
			r, err := agentSkills.PruneSkillTree(base, leaf)
			if err != nil {
				return err
			}
			removed = append(removed, r...)
			continue
		}
		s, err := agentSkills.StaleSkillDirs(base, leaf)
		if err != nil {
			return err
		}
		stale = append(stale, s...)
	}
	for _, p := range written {
		if af.DryRun {
			slog.InfoContext(ctx, "agent install: would write", slog.String("path", p))
			continue
		}
		slog.InfoContext(ctx, "agent install: wrote", slog.String("path", p))
	}
	// Reported at the same level as a write. A silent delete is how a person loses
	// a skill they thought they had.
	for _, p := range removed {
		if af.DryRun {
			slog.InfoContext(ctx, "agent install: would remove skill this binary no longer ships", slog.String("path", p))
			continue
		}
		slog.InfoContext(ctx, "agent install: removed skill this binary no longer ships", slog.String("path", p))
	}
	printAgentInstallNextSteps(af.Dir, written, stale, agent.VariantSimple, af.DryRun)
	return nil
}

// printAgentInstallNextSteps prints an actionable hint after install, gated on
// the user-controlled hints preference so MAGUS_HINTS_ENABLED=false silences it.
func printAgentInstallNextSteps(dir string, written, stale []string, v agent.Variant, dryRun bool) {
	if !interactive.HintsEnabled() || len(written) == 0 {
		return
	}
	// A rehearsal that claims it installed something stops the reader looking for
	// the real run.
	if dryRun {
		interactive.Emit(os.Stderr, fmt.Sprintf("dry run: %d file(s) would be written; nothing was changed. Re-run without --dry-run to apply", len(written)))
		return
	}
	interactive.Emit(os.Stderr, fmt.Sprintf("installed %d file(s); commit them so your team and agents share them", len(written)))
	// Only when there is something to act on. Nothing is stale in the ordinary case -
	// a fresh install, or an upgrade that renamed nothing - and a line that printed on
	// every install is one a reader stops seeing by the third time, which is exactly
	// when it finally matters. One line for the whole set, so a release that renames
	// eight skills does not print eight lines.
	if len(stale) > 0 {
		names := make([]string, 0, len(stale))
		for _, p := range stale {
			names = append(names, filepath.Base(p))
		}
		interactive.Emit(os.Stderr, fmt.Sprintf(
			"%d installed skill(s) this magus no longer ships are still in place, and your agent host still loads them: %s. Remove them with --prune",
			len(stale), strings.Join(names, ", ")))
	}
	reportContextCost(dir, written)
	if v == agent.VariantSimple {
		interactive.Emit(os.Stderr, "each skill also has an always-full <name>-full twin: when you hand work to a smaller model, point it at that name - the primary is the shorter form and bets its reader can re-derive what it drops")
	}
	// MAGUS.md is regenerated for HUMAN readers; the skills send agents to the live
	// verbs instead, because a generated index is only true as of its last run.
	interactive.Emit(os.Stderr, "regenerate MAGUS.md for human readers:  "+hint.DescribeGraph.With("-o", "markdown")+"  (the skills send agents to the live verbs: "+hint.DescribeTargets.String()+", "+hint.Ls.String()+")")
	interactive.Emit(os.Stderr, "safety: consider a line in your repo's agent instruction file so parallel agents cannot wipe each other's work:")
	interactive.Emit(os.Stderr, "  \""+vcsSafetyRule+"\"")
	interactive.Emit(os.Stderr, "starter AGENTS.md you can own and tweak (prints, never writes):  "+hint.AgentSample.String())
	printAgentsBlockToPaste(dir)
}

// printAgentsBlockToPaste offers the managed AGENTS.md block for the developer
// to paste. Silent when their file already carries a current one: 80 lines of
// Markdown on every --force reinstall is how a reader learns to scroll past this
// command's output, including the actionable parts.
func printAgentsBlockToPaste(dir string) {
	verb := "add it to AGENTS.md at your repo root"
	for _, s := range agentSkills.CheckStatuses(dir) {
		if s.Location != agent.AgentsFile {
			continue
		}
		if !s.Stale {
			return
		}
		verb = "your AGENTS.md has an older copy: replace it BETWEEN the markers and leave the rest of the file alone"
	}
	interactive.Emit(os.Stderr, "magus does not write AGENTS.md. That file is yours. If your agent host reads it, "+verb+":")
	fmt.Fprint(os.Stderr, "\n"+agentSkills.AgentsBlock()+"\n")
}

// vcsSafetyRule is the one always-on version-control rule worth carrying in a
// repo's agent instruction file: it stops one agent's whole-tree revert from destroying
// another's uncommitted work. Shared by the install hint and the sample doc.
const vcsSafetyRule = "Version control is the orchestrator's job: do it yourself, never delegate it to a subagent, and never discard or revert uncommitted changes across the whole tree to verify a build - build in place. A whole-tree revert permanently destroys a concurrent agent's uncommitted work."

// agentSampleDoc returns an AGENTS.md starter for a developer to paste and own.
//
// The magus guidance arrives inside its begin/end markers - the same bytes
// install prints - so `magus doctor` can grade it once pasted.
func agentSampleDoc() string {
	return "# AGENTS.md\n\n" +
		"<!-- A starter for AI agents working in this repo. Own and edit this file:\n" +
		"     fill in the project-specific sections below. Everything outside the\n" +
		"     magus:skills markers is yours; magus never writes this file. -->\n\n" +
		"## Project\n\n" +
		"<!-- What this repo is, its primary language(s), and where the entry points\n" +
		"     and top-level layout live. A few sentences. -->\n\n" +
		"## Conventions\n\n" +
		"<!-- The non-obvious house rules an agent cannot infer from the code:\n" +
		"     naming, error handling, comment style, and what NOT to touch. -->\n\n" +
		"## Version control\n\n" +
		"- " + vcsSafetyRule + "\n\n" +
		agentSkills.AgentsBlock()
}

// agentSampleCmd prints agentSampleDoc to stdout, never to a file.
func agentSampleCmd() error {
	fmt.Fprint(os.Stdout, agentSampleDoc())
	return nil
}

// reportContextCost tells the caller how many bytes of instruction the install
// just added, and what the other permutation would have cost.
//
// BYTES, not tokens: a token count is only true for one tokenizer, and these
// files are installed for whatever host the reader uses. Printed at all for
// accountability - a surface that never states its own cost has no pressure on
// it to shrink.
func reportContextCost(dir string, written []string) {
	// Twins are counted separately, not folded in: only the primary is
	// always-loaded, and a twin is a reference copy fetched by name when a reader
	// needs the long form. Summing them would report the always-loaded cost as
	// roughly double what a session actually carries.
	var installed int64
	var twins int64
	for _, rel := range written {
		info, err := os.Stat(filepath.Join(dir, rel))
		if err != nil {
			continue
		}
		if agent.IsFullTwinName(filepath.Base(filepath.Dir(rel))) {
			twins += info.Size()
			continue
		}
		installed += info.Size()
	}
	if installed == 0 {
		return
	}
	msg := fmt.Sprintf("context cost: %s of always-loaded instructions", byteSize(installed))
	// What the always-loaded set would cost if the long form were the one carried. Kept
	// even though it is no longer a flag to choose between, because it is the number that
	// justifies the split: without it the reader cannot see what the shorter form buys.
	if alt, err := agentSkills.VariantSize(agent.VariantFull); err == nil && alt > installed {
		msg += fmt.Sprintf("; the full form would be %s", byteSize(alt))
	}
	if twins > 0 {
		msg += fmt.Sprintf("; plus %s of full twins, loaded only when asked for by name", byteSize(twins))
	}
	interactive.Emit(os.Stderr, msg)
}

// byteSize renders a size the way a reader compares it, not the way a machine
// stores it.
func byteSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f KB", float64(n)/1024)
}

// installTarget resolves a destination into the (base, leaf) pair the catalog
// writers take.
//
// --global is the flag that permits an absolute destination, and it never
// worked: this command let one past its own guard and Catalog.WriteSkillTree
// then refused it unconditionally, so `agent install /abs/path --global` failed
// with the message telling the caller to pass the flag they had just passed.
//
// Fixed HERE rather than by adding an escape hatch to the catalog. That guard is
// the library's own safety property - it also blocks "../../outside", which no
// CLI flag should be able to switch off - so instead the absolute path is split
// into the directory it names and the leaf inside it. The containment check the
// catalog performs is then still meaningful: the write stays under the directory
// the caller actually named.
func installTarget(dir, dest string, global bool) (base, leaf string) {
	if global && filepath.IsAbs(dest) {
		return filepath.Dir(dest), filepath.Base(dest)
	}
	return dir, dest
}
