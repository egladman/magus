package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/notes"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
	"mvdan.cc/sh/v3/syntax"
)

// skillFS holds provider-neutral skill bodies embedded at build time.
//
//go:embed skills
var skillFS embed.FS

// agentsSection is the distilled always-on block installed into AGENTS.md for
// hosts that read that contract instead of skill directories. Same rules as the
// skills, compressed.
//
//go:embed agents-section.md
var agentsSection string

// agentSkills binds the command's embedded source assets to the reusable
// provider-neutral catalog. The CLI owns embedding and presentation; internal/agent
// owns the artifact's rendering, provenance, installation, and verification.
var agentSkills = agent.NewCatalog(skillFS, agentsSection, types.KnowledgeSchemaVersion)

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
	case "-h", "--help", "help":
		agentUsage(os.Stderr)
		return nil
	default:
		return usagef("magus agent: unknown subcommand %q (want install or sample)", args[0])
	}
}

func agentUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus agent <install|sample> [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  install            render the embedded skills and write or stream them")
	fmt.Fprintln(w, "                     into named destinations (.claude/skills, .agents/skills,")
	fmt.Fprintln(w, "                     .opencode/skills, ...)")
	fmt.Fprintln(w, "  sample             print a starter AGENTS.md to stdout to own and tweak;")
	fmt.Fprintln(w, "                     never writes a file")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "magus never writes your AGENTS.md. That file is yours, and an installer")
	fmt.Fprintln(w, "that edits a file you own leaves bytes you did not write and cannot audit.")
	fmt.Fprintln(w, "So `install` PRINTS the managed magus block for you to paste, and only")
	fmt.Fprintln(w, "when your AGENTS.md is missing it or carrying a stale one.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Stdout philosophy: `magus agent` is a pure data generator. To install")
	fmt.Fprintln(w, "skills anywhere your shell can reach, use --tar and pipe to tar:")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "  magus agent install --tar | tar -xf - -C .claude/skills")
	fmt.Fprintln(w, "  magus agent install --tar | tar -xf - -C ~/.config/opencode/skills")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "The write-to-disk form is only for the in-repo, paths-relative-to-<dir>")
	fmt.Fprintln(w, "case, where it preserves the previous one-line ergonomics. Absolute")
	fmt.Fprintln(w, "destinations are refused unless --global is set, to keep magus from")
	fmt.Fprintln(w, "silently writing outside the working tree.")
	fmt.Fprintln(w, "")
	// Listed here because this Usage replaces the FlagSet's own: without it the
	// flags are reachable but undiscoverable, and an agent told to "defer to -h"
	// would conclude they do not exist.
	fmt.Fprintln(w, "install flags:")
	fmt.Fprintln(w, "  --dir <path>   repo directory to install into (default .)")
	fmt.Fprintln(w, "  --force        overwrite existing installed skill files")
	fmt.Fprintln(w, "  --prune        also remove installed skills this binary no longer ships;")
	fmt.Fprintln(w, "                 without it they are reported and left in place. Only skills")
	fmt.Fprintln(w, "                 magus wrote are candidates - a hand-authored one beside them")
	fmt.Fprintln(w, "                 is never touched")
	fmt.Fprintln(w, "  --tar          stream a tar archive to stdout instead of writing files")
	fmt.Fprintln(w, "  --global       allow absolute destination paths in write mode")
}

func agentUsageErr() error {
	agentUsage(os.Stderr)
	return fmt.Errorf("agent: a subcommand is required (try: install)")
}

// hookUsage describes the guard, which is a different command from `agent` despite
// sharing this file: it reads one command or path and answers with a verdict.
func hookUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus hook [--path] [flags]   # the command or path arrives on stdin")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Evaluate ONE shell command, or one file path an edit is about to write,")
	fmt.Fprintln(w, "against this workspace's guard rules, and report a deny/advise/pass")
	fmt.Fprintln(w, "verdict. Built for an agent host's pre-tool-use hook: the input is read")
	fmt.Fprintln(w, "from stdin, so nothing has to be quoted through a shell twice.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Two input shapes are accepted. Plain text is the command (or path) itself.")
	fmt.Fprintln(w, "A JSON envelope from a host that writes one needs no --path and no jq: the")
	fmt.Fprintln(w, "envelope says what is about to run and whether it is a write. An explicit")
	fmt.Fprintln(w, "flag still wins, because a wrapper that passed it meant it.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Examples:")
	// Fprintf with %% : vet rejects a Printf directive inside an Fprintln, and the
	// example is worth more than the convenience. notifyUsage does the same.
	fmt.Fprintf(w, "  printf '%%s' 'go build ./...' | magus hook\n")
	fmt.Fprintf(w, "  printf '%%s' 'MAGUS.md' | magus hook --path\n")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --path                judge the input as a file path an edit is about to")
	fmt.Fprintln(w, "                        write, not as a shell command")
	fmt.Fprintln(w, "  --agent-name <name>   agent host this invocation came from (attribution")
	fmt.Fprintln(w, "                        only; the verdict never reads it)")
	fmt.Fprintln(w, "  --session <id>        the host's own session id, recorded on the event")
	fmt.Fprintln(w, "  --event <name>        the host's hook event name (e.g. PreToolUse)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Global display flags (-o, -s, -q, -v, --tee) are accepted; see `magus -h`.")
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
	// See graph_verify.go: the display flags are global, so every command takes
	// them. `agent install ... -s` previously died on an undefined flag, and with
	// stderr redirected that looked exactly like a successful install - the
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
		slog.InfoContext(ctx, "agent install: wrote", slog.String("path", p))
	}
	// Reported at the same level as a write. A silent delete is how a person loses
	// a skill they thought they had.
	for _, p := range removed {
		slog.InfoContext(ctx, "agent install: removed skill this binary no longer ships", slog.String("path", p))
	}
	printAgentInstallNextSteps(af.Dir, written, stale, agent.VariantSimple)
	return nil
}

// printAgentInstallNextSteps prints an actionable hint after install, gated on
// the user-controlled hints preference so MAGUS_HINTS_ENABLED=false silences it.
func printAgentInstallNextSteps(dir string, written, stale []string, v agent.Variant) {
	if !interactive.HintsEnabled() || len(written) == 0 {
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
	interactive.Emit(os.Stderr, "regenerate MAGUS.md for human readers:  magus describe graph -o markdown  (the skills send agents to the live verbs - magus describe targets, magus ls)")
	interactive.Emit(os.Stderr, "safety: consider a line in your repo's agent instruction file so parallel agents cannot wipe each other's work:")
	interactive.Emit(os.Stderr, "  \""+vcsSafetyRule+"\"")
	interactive.Emit(os.Stderr, "starter AGENTS.md you can own and tweak (prints, never writes):  magus agent sample")
	printAgentsBlockToPaste(dir)
}

// printAgentsBlockToPaste offers the managed AGENTS.md block for the developer
// to paste. Silent when their file already carries a current one: 80 lines of
// Markdown on every --force reinstall is how a reader learns to scroll past this
// command's output, including the actionable parts.
func printAgentsBlockToPaste(dir string) {
	verb := "add it to AGENTS.md at your repo root"
	for _, s := range agentSkills.CheckStatuses(dir) {
		if s.Location != "AGENTS.md" {
			continue
		}
		if !s.Stale {
			return
		}
		verb = "your AGENTS.md has an older copy: replace it BETWEEN the markers and leave the rest of the file alone"
	}
	interactive.Emit(os.Stderr, "magus does not write AGENTS.md - that file is yours. If your agent host reads it, "+verb+":")
	fmt.Fprint(os.Stderr, "\n"+agentSkills.AgentsBlock()+"\n")
}

// vcsSafetyRule is the one always-on version-control rule worth carrying in a
// repo's agent instruction file: it stops one agent's whole-tree revert from destroying
// another's uncommitted work. Shared by the install hint and the sample doc.
const vcsSafetyRule = "Version control is the orchestrator's job: do it yourself, never delegate it to a subagent, and never discard or revert uncommitted changes across the whole tree to verify a build - build in place. A whole-tree revert permanently destroys a concurrent agent's uncommitted work."

// agentSampleDoc returns an AGENTS.md starter for a developer to paste and own.
//
// The magus guidance arrives inside its begin/end markers - the same bytes
// install prints - so `magus graph verify` can grade it once pasted.
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

// guardVerdict is the neutral result of evaluating one shell command: exactly
// one decision, carrying the field that decision needs. This envelope is the
// stable contract an agent host's hook config shapes with -o template (or
// parses from -o json); the host-specific response dialects live in the
// documentation, never in code.
type guardVerdict struct {
	SchemaVersion int    `json:"schema_version"`
	Decision      string `json:"decision"`          // one of agent.GuardDecisions
	Reason        string `json:"reason,omitempty"`  // deny: the block reason, written for the model
	Context       string `json:"context,omitempty"` // advise: context to inject alongside the allowed call
}

// hookCmd implements `magus hook`: evaluate one shell command or file path read
// from stdin and emit a verdict. The caller owns extraction from its
// host-specific event shape; magus owns only the host-neutral policy.
//
// A guard must FAIL OPEN: an empty or unreadable input is a pass, never an error
// that would block every tool call.
func hookCmd(ctx context.Context, in io.Reader, out io.Writer, args []string) error {
	fset := flag.NewFlagSet("hook", flag.ContinueOnError)
	hf := gen.BindHook(fset)
	// Attribution, not policy: these name WHO produced the observation, and the
	// guard's verdict never reads them. Every one is optional and unvalidated -
	// including the host name, which is an opaque label the caller chooses rather
	// than a set magus knows, because a magus that enumerated hosts would need a
	// release per host. A wrapper that cannot extract a session id must still get
	// a verdict; erroring here would block a tool call over metadata.
	// The whole display set, not a hand-rolled -o: this command used to define
	// its own output flag and so silently lacked -s, -q, -v and --tee. That gap
	// is the reason for the rule - a flag accepted on most commands teaches
	// callers it is unreliable everywhere.
	bindDisplayFlags(fset)
	// hookUsage, not agentUsage: `hook` and `agent` share this file, and pointing at the
	// wrong one makes `magus hook -h` answer a question nobody asked.
	fset.Usage = func() { hookUsage(os.Stderr) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if len(fset.Args()) != 0 {
		return usagef("magus hook: takes no positional arguments (read the command or path from stdin)")
	}
	opts, err := ResolveOutput(global.output)
	if err != nil {
		return err
	}

	input, hasInput := readGuardInput(in)
	who := hookAttribution{Host: hf.AgentName, Session: hf.Session, Event: hf.Event}
	// A host that writes its hook payload as JSON needs no jq and no --path: the envelope
	// says what is about to run and whether it is a write. Explicit flags still win, since
	// a wrapper that passed them meant them.
	if req, isEnvelope := decodeHookEnvelope(input.Value); isEnvelope {
		input.Value = req.Value
		if req.IsPath {
			hf.Path = true
		}
		if who.Session == "" {
			who.Session = req.Who.Session
		}
		if who.Event == "" {
			who.Event = req.Who.Event
		}
		if req.IsSpawn {
			// A delegation carries no verdict, so it returns the pass every other
			// non-finding does and never reaches the guard. Handled here rather than
			// beside the two guard arms because the whole point is that nothing judges
			// it: the handed context is prose, and a prompt that merely MENTIONS a
			// denied command would otherwise block the delegation that describes it.
			appendHookSpawn(ctx, req, who)
			return writeGuardVerdict(out, opts,
				guardVerdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "pass"})
		}
	}
	verdict := guardVerdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "pass"}
	if hf.Path {
		if hasInput {
			// The generated-output rule is definitive (it reads declared globs), so it
			// speaks first; the memory nudge is a heuristic on the filename and only
			// fills the silence it leaves.
			context := adviseGeneratedWrite(ctx, input.Value)
			// The notes rule DENIES, so it is checked before the advisories: a verdict
			// that blocks is not something to fall through to. It sits after the
			// generated-output rule only because a path cannot honestly be both, and if
			// it somehow were, the regeneration answer is the more actionable one.
			if context == "" {
				if reason := denyNotesWrite(input.Value); reason != "" {
					verdict.Decision = "deny"
					verdict.Reason = reason
				}
			}
			if verdict.Decision == "pass" && context == "" {
				context = adviseInstalledSkillWrite(input.Value)
			}
			if verdict.Decision == "pass" && context == "" {
				context = adviseMemoryWrite(input.Value)
			}
			if verdict.Decision == "pass" && context != "" {
				verdict.Decision = "advise"
				verdict.Context = context
			}
		}
		appendHookActivity(ctx, input, who, true, verdict)
		if err := writeGuardVerdict(out, opts, verdict); err != nil {
			return err
		}
		return enforceVerdict(opts, verdict)
	}
	if hasInput {
		switch v := evaluateBashGuard(input.Value); {
		case v.Deny != "":
			verdict.Decision = "deny"
			verdict.Reason = v.Deny
		case v.Context != "":
			verdict.Decision = "advise"
			verdict.Context = v.Context
		}
	}
	appendHookActivity(ctx, input, who, false, verdict)
	if err := writeGuardVerdict(out, opts, verdict); err != nil {
		return err
	}
	return enforceVerdict(opts, verdict)
}

// guardDenyExitCode is what a denied command exits with.
//
// A hook that reports a deny and exits 0 blocks NOTHING - the host sees success
// and runs the command anyway, so the guard looks enforced and is not. 2 rather
// than 1 is what the dominant host reads as "block and show the reason to the
// model". The collision with the usage code is harmless: a guard that could not
// parse its input has not judged the command either.
const guardDenyExitCode = 2

// enforceVerdict turns a deny into a blocking exit. Applies to every format: an
// `-o json` caller with a zero status would be told the same lie in a different
// shape.
//
// The reason reaches stderr only when stdout does not already carry it as prose,
// i.e. every format but text. The guard templates read the verdict off stdout and
// one discards stderr outright, so an unconditional copy printed a kilobyte-plus
// reason twice to an audience with a context budget.
func enforceVerdict(opts OutputOptions, verdict guardVerdict) error {
	if verdict.Decision != "deny" {
		return nil
	}
	if opts.Format != FormatText {
		fmt.Fprintln(os.Stderr, verdict.Reason)
	}
	return errSilent{exitCode: guardDenyExitCode}
}

// writeGuardVerdict renders a verdict through the standard output arm.
func writeGuardVerdict(out io.Writer, opts OutputOptions, verdict guardVerdict) error {
	switch opts.Format {
	case FormatText:
		switch verdict.Decision {
		case "deny":
			fmt.Fprintln(out, "deny: "+verdict.Reason)
		case "advise":
			fmt.Fprintln(out, "advise: "+verdict.Context)
		default:
			fmt.Fprintln(out, "pass")
		}
		return nil
	case FormatName:
		fmt.Fprintln(out, verdict.Decision)
		return nil
	}
	return writeFormatted(out, opts, verdict)
}

// adviseGeneratedWrite explains why editing path is wasted effort, or "" when
// there is nothing to say. Not a heuristic: magus knows every target's declared
// outputs, so a role=output path is generated by definition.
//
// It teaches rather than blocks - a hand-edited generated file is wasteful, not
// destructive. Silent on every uncertainty, because an advisory fired on a guess
// trains the reader to ignore it.
func adviseGeneratedWrite(ctx context.Context, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	// No root override: FindRoot walks up from the CWD, which is where the hook
	// runs, so a nested project resolves to its own workspace.
	ws, err := inspectWorkspace(ctx, "")
	if err != nil || ws == nil {
		return ""
	}
	// A cancelled ctx folds into the same silent-on-uncertainty contract as the
	// error above: this advisory has no error path of its own.
	files, err := ws.ClassifyFiles(ctx, []string{path})
	if err != nil || len(files) != 1 || files[0].Role != "output" {
		return ""
	}
	f := files[0]
	owner := f.Project
	if owner == "" {
		owner = "."
	}
	return fmt.Sprintf("magus workspace: %s is a DECLARED OUTPUT of project %s - it is generated, and the next run of its producing target overwrites whatever you write there. This is not a style rule: magus reads the target's declared output globs, so the classification is definitive. Change the SOURCE that produces it instead, then run `magus run generate %s` (or the producing target) and commit the regenerated file together with your source change. `magus describe file %s` classifies any path. Load the magus-vcs-hygiene skill if not already loaded.", f.Path, owner, owner, f.Path)
}

// denyNotesWrite blocks a write into the workspace's declared notes store, or returns ""
// for every other path.
//
// This is the only DENY on the path surface, and it does not fit either of the guard's two
// standing triggers, so it is worth saying plainly why it is here. It is not irreversible:
// a note in git is recoverable. It does not have an exact equivalent either: `magus memory
// put` is where an agent's thought belongs, but memory is user-local while notes are
// shared, so the substitute is not equal for something meant for the team.
//
// The trigger is a third one - the artifact's value depends on a guarantee about WHO wrote
// it, and undoing the write does not restore the guarantee. A note is the one thing in the
// graph that is not derived from the workspace: nothing in the repo corroborates it, now or
// in a year, so its only provenance is the person who wrote it. One agent-written note does
// not damage that note, it damages a reader's ability to trust ANY note without checking
// blame - and a note of uncertain authorship is not degraded, it is worthless.
//
// Two honest limits. The guard is not a security boundary (see TestGuardKnownHoles): this
// is a habit rail, and the gate that holds is a check on the pull-request path. And on a
// host with no pre-write file hook - Cursor - the deny arrives after the write has landed,
// which its template records as deny=human rather than papering over.
//
// Silent unless the store is DECLARED. A deny fired on a guessed location would block work
// in a workspace that never opted in, which is far worse than an advisory fired on a guess.
// workspaceDeclaresNotes reports whether THIS repository's own magus.yaml declares a shared
// notes store. A read failure reports false, degrading to the on-disk check rather than
// denying writes in a workspace whose intent cannot be read.
func workspaceDeclaresNotes(root string) bool {
	cfg, err := config.LoadWorkspaceOnly(root)
	if err != nil {
		return false
	}
	return strings.TrimSpace(cfg.Knowledge.Notes.Shared) != ""
}

func denyNotesWrite(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	declared := strings.TrimSpace(globalCfg.Knowledge.Notes.Shared)
	if declared == "" {
		return ""
	}
	root, err := magus.FindRoot("")
	if err != nil {
		return ""
	}
	dir, err := notes.Dir(root, notes.ScopeShared, declared)
	if err != nil {
		return ""
	}
	// The opt-in is the repository's OWN magus.yaml, and it holds from the moment the key is
	// committed. Gating on the directory existing instead let an agent author the store's
	// FIRST note - measured 2026-08-13, a write to notes/<name>.md passed - after which the
	// deny switched on and protected the forgery it had just admitted.
	//
	// A declaration from anywhere else (an explicit --config, user-global) is in effect in
	// every workspace on the machine, so that one still needs the store to exist, or one
	// setting would deny writes in repos that never opted in.
	if !workspaceDeclaresNotes(root) {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return ""
		}
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
	}
	// Compare REAL paths. A host hands over whatever path its editor tool used, and on
	// macOS /var is a symlink to /private/var, so a tmpdir-rooted workspace yields a
	// declared store under one spelling and an incoming write under the other. Comparing
	// them literally makes the rule silently pass - a deny that fails open is worse than
	// no deny, because it looks enforced.
	rel, err := filepath.Rel(resolveSymlinks(dir), resolveSymlinks(filepath.Clean(abs)))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return fmt.Sprintf("magus workspace: %s is in this workspace's NOTES store, which is human-authored by design - agents read notes and never write them.\n"+
		"A note is the one thing in the knowledge graph that is not derived from the repository: nothing here can corroborate it later, so its only provenance is the person who wrote it and signed the commit. A note of uncertain authorship is not a weaker note, it is a worthless one.\n"+
		"If you are recording a DECISION ABOUT THIS WORKSPACE, put it in the handoff journal instead: `magus memory put <name>`, which is the agent-writable store and is anchored to refs a later reader can re-run.\n"+
		"If this genuinely belongs in the notes, say so and let the person write it: `magus notes edit %s` opens their editor. Read the store with `magus notes ls` and `magus notes get <name>`.", path, strings.TrimSuffix(filepath.Base(path), ".md"))
}

// resolveSymlinks canonicalizes as much of path as exists, returning it unchanged when
// nothing can be resolved. The file being judged has not been written yet, so the deepest
// EXISTING ancestor is what can be resolved; the remainder is appended verbatim.
func resolveSymlinks(path string) string {
	rest := ""
	for cur := path; ; {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return path // nothing along the way exists; compare what we were given
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// adviseMemoryWrite nudges a magus-domain decision toward `magus memory put`
// when the write lands in a cross-host instruction file, or "" otherwise.
//
// Capture, not replication: host instructions belong in the file. The point is
// that a decision ABOUT THE WORKSPACE outlives a per-checkout, per-host file, so
// the advisory names both destinations rather than arguing against one.
func adviseMemoryWrite(path string) string {
	// Matched as a bare filename stem, which is the sanctioned form: these name
	// well-known files on disk rather than branching on which host is running.
	// The .md check keeps a template or a sibling extension (agents.md.tmpl,
	// agents.mdx) out of it.
	base := strings.ToLower(filepath.Base(strings.TrimSpace(path)))
	if filepath.Ext(base) != ".md" {
		return ""
	}
	switch strings.TrimSuffix(base, ".md") {
	case "agents", "claude":
	default:
		return ""
	}
	return "magus workspace: this is a per-host instruction file - it lives in one checkout and one host's conventions, and a second worktree or a different agent host does not see it. If what you are recording is a DECISION ABOUT THIS WORKSPACE (a target, a saved query, an output ref, a doc), put it in the handoff journal too: `magus memory put <name>` keeps it outside the checkout, where it survives worktrees, sessions, and hosts. Host instructions are right where they are; workspace decisions are not. Load the magus-handoff-journal skill if not already loaded."
}

// adviseInstalledSkillWrite explains that an installed skill is generated, or
// "" when the path is not one. The other path advisories catch a write that is
// wasted; this one catches a write that DISAPPEARS - `graph verify` reports it
// as drift and the next `install --force` erases it.
//
// The STAMP is the discriminator, not the path: a workspace's own skill sits in
// the same directory, and telling an author their file is generated would be
// worse than saying nothing.
//
// Unreachable in magus's own tree, which is worth knowing before hunting a bug:
// this repo declares its installed skills as outputs, so adviseGeneratedWrite
// claims the path first and can name the producing target.
func adviseInstalledSkillWrite(path string) string {
	clean := filepath.ToSlash(strings.TrimSpace(path))
	if filepath.Base(clean) != "SKILL.md" {
		return ""
	}
	installed := false
	for _, dir := range agent.WellKnownSkillDirs() {
		if strings.Contains(clean, dir+"/") {
			installed = true
			break
		}
	}
	if !installed {
		return ""
	}
	body, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(body), "source: magus") {
		return ""
	}
	return "magus workspace: that file is an INSTALLED skill - magus generates it from its own embedded sources and stamps it with a content digest. Editing it does not fail loudly, it fails silently in two ways: `magus graph verify` reports the file as stale rather than reading what you wrote, and the next `magus agent install <dir> --force` overwrites it. Rules that belong to THIS workspace go in a local skill beside the installed ones instead - a directory magus does not ship, conventionally magus-local-development, which install and verify both leave alone by construction. Stamp each rule with its evidence and the condition that retires it. Load the magus-workspace-rules skill for the format and the rest of the method."
}

// guardInput keeps the resolved command/path distinct from its rendering and
// audit policy. The hook's input is deliberately plain text: host event parsing
// belongs in the host wrapper, not in a durable magus CLI contract.
type guardInput struct {
	Value string
}

func readGuardInput(in io.Reader) (guardInput, bool) {
	b, err := io.ReadAll(in)
	value := strings.TrimSpace(string(b))
	if err != nil || value == "" {
		return guardInput{}, false
	}
	return guardInput{Value: value}, true
}

// hookEnvelope is the JSON an agent host writes to a hook's stdin: which tool is about to
// run and with what. Only the fields the guard needs are modeled; everything else in the
// payload is ignored rather than rejected, since a host is free to add to it.
type hookEnvelope struct {
	HookEventName string `json:"hook_event_name"`
	SessionID     string `json:"session_id"`
	ToolName      string `json:"tool_name"`
	ToolInput     struct {
		Command  string `json:"command"`
		FilePath string `json:"file_path"`
		// A delegation: the context an orchestrator is about to hand a sub-agent, plus
		// whatever the host calls the callee. Field PATHS, not a host name - the same line
		// the two fields above already draw. magus does not know which tool produces them
		// and never switches on ToolName; a payload carrying a prompt IS a spawn.
		Prompt       string `json:"prompt"`
		Description  string `json:"description"`
		SubagentType string `json:"subagent_type"`
	} `json:"tool_input"`
}

// decodeHookEnvelope pulls the thing to judge out of a host's hook payload,
// reporting whether the input was an envelope at all.
//
// Reading it here keeps `jq` off the critical path of every tool call, and lets
// attribution come from the payload rather than from flags a wrapper has to
// remember. A payload carrying file_path rather than command is a write, so the
// envelope also answers the --path question.
//
// A payload carrying a PROMPT rather than either is a delegation handoff: there is nothing to
// judge, and the context being handed over is recorded instead. It is tested last on purpose, so
// that adding this branch cannot change the verdict on any payload the guard already judged.
//
// Anything that is not an object with a usable tool_input is judged as the
// literal text it is.
func decodeHookEnvelope(raw string) (hookRequest, bool) {
	if !strings.HasPrefix(raw, "{") {
		return hookRequest{}, false
	}
	var env hookEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return hookRequest{}, false
	}
	req := hookRequest{Who: hookAttribution{Session: env.SessionID, Event: env.HookEventName}}
	switch {
	case env.ToolInput.Command != "":
		req.Value = env.ToolInput.Command
	case env.ToolInput.FilePath != "":
		req.Value, req.IsPath = env.ToolInput.FilePath, true
	case env.ToolInput.Prompt != "":
		req.Value, req.IsSpawn = env.ToolInput.Prompt, true
		req.Tool = env.ToolName
		// Most specific label first. A sub-agent TYPE names what was delegated to and repeats
		// across spawns, so it groups a delegation feed; a description is per-spawn prose; the
		// tool name is the last resort that at least says a spawn happened.
		for _, label := range []string{env.ToolInput.SubagentType, env.ToolInput.Description, env.ToolName} {
			if label != "" {
				req.Child = label
				break
			}
		}
	default:
		return hookRequest{}, false
	}
	return req, true
}

// hookRequest is what a host's payload asked the guard to judge: the text, whether it is a
// path rather than a command, and who reported it. A spawn asks for nothing to be judged - it
// carries the handed context and the callee's label, and is recorded rather than evaluated.
type hookRequest struct {
	Value   string
	IsPath  bool
	IsSpawn bool
	Tool    string
	Child   string
	Who     hookAttribution
}

// hookAttribution is what the host wrapper knows about itself and cannot be
// derived here: a hook runs as a short-lived client process with no way to
// discover which agent host started it. It travels beside the input rather than
// inside guardInput because the guard's verdict must never depend on it.
type hookAttribution struct {
	Host    string
	Session string
	Event   string
}

type hookActivityLocation struct {
	base      string
	workspace string
}

type hookActivityLocationKey struct{}

// appendHookActivity contributes a best-effort, normalized observation to the same durable
// trail used by MCP and daemon actions. It deliberately runs before rendering the guard response:
// the host may choose not to execute a denied command, and a pre-hook never learns the eventual
// exit status. An audit failure must therefore be invisible to both the verdict and the command.
func appendHookActivity(ctx context.Context, input guardInput, who hookAttribution, asPath bool, verdict guardVerdict) {
	if input.Value == "" {
		return
	}
	location := hookActivityTrail(ctx)
	if location.base == "" {
		return
	}
	tool := "shell.command"
	if asPath {
		tool = "file.write"
	}
	command := trail.AgentCommand{
		Actor:     "agent",
		Workspace: location.workspace,
		Host:      who.Host,
		Session:   who.Session,
		Event:     who.Event,
		Tool:      tool,
		Decision:  verdict.Decision,
		Reason:    verdict.Reason,
		Context:   verdict.Context,
	}
	if asPath {
		command.Path = input.Value
	} else {
		command.Command = input.Value
	}
	trail.AppendAgentCommand(ctx, location.base, command)
}

// appendHookSpawn records a delegation handoff into the same trail, so a person auditing the
// activity log later can see WHAT CONTEXT an orchestrator handed a sub-agent, not merely that it
// spawned one. Like appendHookActivity it is best-effort and cannot fail the tool call; unlike it
// there is no verdict to record, because a spawn is not a guard surface.
func appendHookSpawn(ctx context.Context, req hookRequest, who hookAttribution) {
	if req.Value == "" {
		return
	}
	location := hookActivityTrail(ctx)
	if location.base == "" {
		return
	}
	trail.AppendAgentSpawn(ctx, location.base, trail.AgentSpawn{
		Actor:     "agent",
		Workspace: location.workspace,
		Host:      who.Host,
		Session:   who.Session,
		Event:     who.Event,
		Tool:      req.Tool,
		Child:     req.Child,
		Context:   req.Value,
	})
}

// hookActivityTrail resolves the local workspace cache because a hook runs as a short-lived
// client process, outside the daemon's memory. Tests can pin a temporary base through context so
// a guard unit test never writes its checkout's real activity trail.
func hookActivityTrail(ctx context.Context) hookActivityLocation {
	if location, ok := ctx.Value(hookActivityLocationKey{}).(hookActivityLocation); ok {
		return location
	}
	root, err := magus.FindRoot("")
	if err != nil {
		return hookActivityLocation{}
	}
	cacheDir, err := magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg))
	if err != nil {
		return hookActivityLocation{}
	}
	return hookActivityLocation{base: cacheDir, workspace: root}
}

// bashGuardVerdict classifies one Bash command line. Deny blocks the call with a
// reason the model sees; Context lets it proceed and injects a reminder.
type bashGuardVerdict struct {
	Deny    string
	Context string
}

// cmdPos anchors a pattern to a COMMAND position - line start or just after a
// shell separator - so a pattern cannot match its own name appearing as text.
// `go test` and `git add -A` show up constantly in test data and commit messages.
//
// Deliberately NOT applied to the whole-tree VCS patterns: those deny work that
// cannot be recovered, so a rare false positive there is the safe direction.
//
// A separator preceded by a BACKSLASH is an escape inside a quoted argument, not
// a separator (commonly a grep alternation). RE2 has no lookbehind, so the
// preceding char is consumed by a negated class; `^` keeps the start-of-string
// case.
const cmdPos = `(?:^|[^\\][;&|(]\s*|\s&&\s*|\s\|\|\s*|` + "`" + `)\s*`

// A pass-through wrapper runs ANOTHER command, with the environment or timeout
// adjusted first. It is never itself the finding: the guard peels it off and
// judges the payload, so an unapproved launcher cannot tunnel a raw tool past
// the guard.
//
// A launcher's declared task subcommand is deliberately absent: it runs a task,
// not a smuggled command, and peeling it would misattribute the task's contents.
var guardWrappers = map[string]bool{
	"env": true, "nohup": true, "command": true, "exec": true,
	"time": true, "timeout": true, "nice": true, "stdbuf": true,
	"xargs": true, "setsid": true, "sudo": true, "doas": true,
	"mise": true, "rtx": true,
	"sh": true, "bash": true, "zsh": true, "ksh": true, "dash": true,
	"eval": true,
}

// guardCommand is one command the line would actually run: the program, and its
// arguments with quoting already resolved.
type guardCommand struct {
	Name string
	Args []string
}

// parseGuardCommands resolves a shell line into every command it would run.
//
// A PARSER rather than a pattern because every wrong verdict this guard has
// produced was a tokenizing mistake, not a policy one: a regex cannot tell a
// pipe from a pipe inside quotes, an assignment prefix from an argument, or see
// into a shell's own -c argument. An AST answers all three structurally.
//
// The bool is false when the line does not parse, and the caller skips the
// raw-tool rules rather than guessing - less of a bypass than it looks, since
// shell that does not parse does not run either.
func parseGuardCommands(command string) ([]guardCommand, bool) {
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return nil, false
	}
	var out []guardCommand
	syntax.Walk(f, func(n syntax.Node) bool {
		if call, ok := n.(*syntax.CallExpr); ok {
			// Assigns are deliberately not consulted: the parser has already
			// separated the VAR=value prefix from the command, which is the
			// entire reason for parsing.
			out = append(out, peelWrappers(literalWords(call.Args))...)
		}
		return true
	})
	return out, true
}

// literalWords renders each word down to the text the shell would pass, as far
// as that is knowable without running anything. A word whose value comes from a
// parameter or a command substitution renders empty: its VALUE is unknown, but a
// command substitution's own commands are separate CallExpr nodes that Walk
// reaches independently, so nothing is lost by declining to guess here.
func literalWords(words []*syntax.Word) []string {
	out := make([]string, 0, len(words))
	for _, w := range words {
		out = append(out, literalWord(w.Parts))
	}
	return out
}

func literalWord(parts []syntax.WordPart) string {
	var b strings.Builder
	for _, part := range parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			b.WriteString(literalWord(p.Parts))
		}
	}
	return b.String()
}

// peelWrappers reduces a wrapper invocation to the command it wraps, repeatedly,
// so a stack of them reduces to the program that will actually run. A -c payload
// is parsed as its own script, so it contributes the commands it contains rather
// than one opaque string.
func peelWrappers(words []string) []guardCommand {
	for len(words) > 0 {
		name := path.Base(words[0])
		switch {
		case !guardWrappers[name]:
			return []guardCommand{{Name: name, Args: words[1:]}}

		case name == "mise" || name == "rtx":
			// A launcher may carry tool selectors before `--`; its short form is
			// accepted too. Both then pass the command after `--` through unchanged.
			if len(words) > 1 && (words[1] == "exec" || words[1] == "x") {
				if i := slices.Index(words, "--"); i >= 0 {
					words = words[i+1:]
					continue
				}
			}
			return []guardCommand{{Name: name, Args: words[1:]}}

		case guardShells[name]:
			script, ok := shellDashC(words[1:])
			if !ok {
				return []guardCommand{{Name: name, Args: words[1:]}}
			}
			inner, _ := parseGuardCommands(script)
			return inner

		case name == "eval":
			// eval concatenates its arguments and runs the result as a script.
			// Worth following for the same reason as `sh -c`: when the words are
			// literal it is an exact synonym for running them directly. When they
			// come from a variable, literalWord already rendered them empty and
			// the reparse finds nothing, which is the honest answer.
			inner, _ := parseGuardCommands(strings.Join(words[1:], " "))
			return inner

		default:
			// env, timeout, nice, stdbuf, xargs, nohup, command, exec, time:
			// step over the wrapper's own flags and operands to reach the
			// program it runs.
			rest := skipWrapperArgs(name, words[1:])
			if len(rest) == 0 {
				return []guardCommand{{Name: name, Args: words[1:]}}
			}
			words = rest
		}
	}
	return nil
}

var guardShells = map[string]bool{"sh": true, "bash": true, "zsh": true, "ksh": true, "dash": true}

// guardWrapperValueFlags are wrapper flags that consume the NEXT word, so the
// scan does not mistake that word for the wrapped program. `env -u GOROOT go
// test` is the case that matters here.
var guardWrapperValueFlags = map[string]bool{
	"-u": true, "-C": true, "-S": true, "-n": true, "-I": true,
	"-L": true, "-P": true, "-d": true, "-s": true, "-k": true,
	"--signal": true, "--kill-after": true,
}

// skipWrapperArgs consumes a wrapper's own flags and operands, returning the
// words from the wrapped program onward.
func skipWrapperArgs(wrapper string, words []string) []string {
	for len(words) > 0 {
		w := words[0]
		switch {
		case strings.HasPrefix(w, "-"):
			if guardWrapperValueFlags[w] && len(words) > 1 {
				words = words[2:]
				continue
			}
			words = words[1:]
		case strings.Contains(w, "=") && !strings.HasPrefix(w, "/"):
			// `env VAR=value cmd`: an assignment operand, not the program.
			words = words[1:]
		case wrapper == "timeout":
			// timeout's first non-flag operand is the DURATION, not the program.
			words = words[1:]
			wrapper = ""
		default:
			return words
		}
	}
	return nil
}

// shellDashC returns the script argument of a `-c` invocation. The flag may be
// bundled (`sh -ec ...`) and other options may precede it.
func shellDashC(args []string) (string, bool) {
	for i, a := range args {
		if !strings.HasPrefix(a, "-") {
			return "", false
		}
		if strings.Contains(a, "c") && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// guardToolMatch is one command spell operation Magus can run on the caller's
// behalf. It is derived from the registered spell catalog, never a hand-kept
// list in the guard: adding a spell operation automatically teaches the hook
// which raw command it replaces.
type guardToolMatch struct {
	spell     string
	operation string
}

// guardTextFilters are the shell commands whose purpose is to trim, slice, or
// search text. Piping magus into one is always a missing output flag.
//
// `jq` and `magus` are deliberately absent. Both consume a CONTRACT rather than
// scraping a layout - `jq` over `-o json`, and magus-into-magus over `--stdin` -
// which is composition, the opposite of the antipattern. `tee` is absent too: it
// duplicates a stream without trimming it.
var guardTextFilters = map[string]bool{
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true,
	"head": true, "tail": true, "awk": true, "sed": true,
	"cut": true, "sort": true, "uniq": true, "wc": true, "column": true,
}

// magusPipedToFilter reports a magus command whose output is being trimmed by a
// shell text filter. Denied rather than advised: as an advisory it fired
// repeatedly and was read straight past, the same trained-reflex result the
// raw-tool advisory produced.
//
// `magus query output <ref>` is the ONE exemption - it returns a raw captured
// log with no schema for magus to project, so searching it is a real need. Every
// other verb emits a structured record that -o shapes exactly.
func magusPipedToFilter(command string) bool {
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return false
	}
	found := false
	syntax.Walk(f, func(n syntax.Node) bool {
		pipe, ok := n.(*syntax.BinaryCmd)
		if !ok || pipe.Op != syntax.Pipe {
			return true
		}
		if trimmableMagus(lastOfPipeline(pipe.X)) && isTextFilter(firstOfPipeline(pipe.Y)) {
			found = true
		}
		return true
	})
	return found
}

// magusRedirected reports a magus command whose stdout or stderr is being sent
// to a file, to /dev/null, or folded together with 2>&1.
//
// Denied for the pipe rule's reason. `--silent > /dev/null 2>&1` is the worst
// case: silent mode stays quiet UNTIL something fails, then prints the likely
// diagnostics and the full-log path, and the redirect discards exactly that.
//
// `magus query output <ref>` is exempt, as with the pipe rule. Note --tee is NOT
// the escape hatch a reader might assume - it mirrors STRUCTURED output only -
// so the message points at the persisted log instead.
func magusRedirected(command string) bool {
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return false
	}
	found := false
	syntax.Walk(f, func(n syntax.Node) bool {
		stmt, ok := n.(*syntax.Stmt)
		if !ok || len(stmt.Redirs) == 0 || !trimmableMagus(stmtCommands(stmt)) {
			return true
		}
		for _, r := range stmt.Redirs {
			switch r.Op {
			// Output redirects only. A HEREDOC or an input redirect feeds magus
			// rather than hiding what it said, so neither is this rule's business.
			case syntax.RdrOut, syntax.AppOut, syntax.DplOut, syntax.RdrAll, syntax.AppAll:
				found = true
			}
		}
		return true
	})
	return found
}

// throwawayDirRe matches a path under a temp root, or any path with a scratchpad
// segment - the places a COPY of a workspace gets made rather than checked out.
var throwawayDirRe = regexp.MustCompile(`^(/private)?/(tmp|var/folders)/|/scratchpad(/|$)`)

// assignmentRe and cdTargetRe recover `NAME=value` and the argument of a `cd`.
var (
	assignmentRe = regexp.MustCompile(`(?:^|[;&|]\s*|\s)([A-Za-z_]\w*)=("?)([^"'\s;&|]+)`)
	cdTargetRe   = regexp.MustCompile(`\bcd\s+["']?([^"'\s;&|]+)`)
)

// magusInThrowawayCopy reports a magus command being run from a COPY of a
// workspace in a temp or scratchpad directory.
//
// Denied because of what it produces: a verdict about a tree nobody will ship. A
// gate that passes in a stale duplicate is worse than no gate. It also splits the
// cache, strands generated files inside the copy, and duplicates spell sources
// (MGS1002).
//
// Variable assignments are resolved too, since the observed shape chains a whole
// pipeline onto one - keying on a literal `cd /tmp/...` would miss it.
//
// A genuinely different workspace is `--root <path>`, which keeps one cache.
func magusInThrowawayCopy(command string) bool {
	if !mentionsMagusCommand(command) {
		return false
	}
	vars := map[string]string{}
	for _, m := range assignmentRe.FindAllStringSubmatch(command, -1) {
		vars[m[1]] = m[3]
	}
	for _, m := range cdTargetRe.FindAllStringSubmatch(command, -1) {
		if throwawayDirRe.MatchString(expandGuardVars(m[1], vars)) {
			return true
		}
	}
	return false
}

// expandGuardVars substitutes $NAME and ${NAME} from assignments made earlier on
// the same line. Anything it cannot resolve is left as written, so an unknown
// variable simply fails to match rather than matching everything.
func expandGuardVars(s string, vars map[string]string) string {
	for name, val := range vars {
		s = strings.ReplaceAll(s, "${"+name+"}", val)
		s = strings.ReplaceAll(s, "$"+name, val)
	}
	return s
}

// mentionsMagusCommand reports whether the line actually RUNS magus, so the
// throwaway-copy rule cannot fire on a line that merely names a temp path.
func mentionsMagusCommand(command string) bool {
	cmds, parsed := parseGuardCommands(command)
	if !parsed {
		return false
	}
	return slices.ContainsFunc(cmds, func(c guardCommand) bool { return c.Name == "magus" })
}

// lastOfPipeline and firstOfPipeline resolve the commands immediately either
// side of one pipe, descending through a longer pipeline to reach them.
func lastOfPipeline(s *syntax.Stmt) []guardCommand {
	if bc, ok := s.Cmd.(*syntax.BinaryCmd); ok && bc.Op == syntax.Pipe {
		return lastOfPipeline(bc.Y)
	}
	return stmtCommands(s)
}

func firstOfPipeline(s *syntax.Stmt) []guardCommand {
	if bc, ok := s.Cmd.(*syntax.BinaryCmd); ok && bc.Op == syntax.Pipe {
		return firstOfPipeline(bc.X)
	}
	return stmtCommands(s)
}

func stmtCommands(s *syntax.Stmt) []guardCommand {
	call, ok := s.Cmd.(*syntax.CallExpr)
	if !ok {
		return nil
	}
	return peelWrappers(literalWords(call.Args))
}

func trimmableMagus(cmds []guardCommand) bool {
	for _, c := range cmds {
		if c.Name != "magus" {
			continue
		}
		if len(c.Args) >= 2 && c.Args[0] == "query" && c.Args[1] == "output" {
			continue
		}
		return true
	}
	return false
}

func isTextFilter(cmds []guardCommand) bool {
	return slices.ContainsFunc(cmds, func(c guardCommand) bool { return guardTextFilters[c.Name] })
}

// firstRawToolDenied parses the line and returns the FIRST command it would run
// that magus already covers. A line that does not parse skips this rule.
//
// It returns the command rather than a bool so the verdict can name it: told only
// that `bash -c '...'` was denied, a reader has to work out which part offended,
// and a denial becomes a wrapper hunt instead of a correction.
func firstRawToolDenied(command string) (guardCommand, bool) {
	cmds, ok := parseGuardCommands(command)
	if !ok {
		return guardCommand{}, false
	}
	for _, c := range cmds {
		if _, ok := rawToolMatch(c); ok {
			return c, true
		}
	}
	return guardCommand{}, false
}

// explainDeny prefixes a rule's reason with the resolved command that tripped
// it, and says so explicitly when that differs from what was typed - which is
// the whole point of peeling wrappers, made visible instead of implied.
func explainDeny(typed string, c guardCommand, reason string) string {
	resolved := strings.TrimSpace(c.Name + " " + strings.Join(c.Args, " "))
	var b strings.Builder
	b.WriteString("magus guard denied `" + resolved + "`")
	if strings.TrimSpace(typed) != resolved {
		b.WriteString(", which is what `" + strings.TrimSpace(typed) + "` resolves to once wrappers and quoting are stripped. The wrapper is not the problem and re-wrapping will not help: the guard reads the command being RUN")
	}
	b.WriteString(".\n\n")
	b.WriteString(reason)
	return b.String()
}

// rawToolDenied reports whether one resolved command has a registered spell-op
// equivalent. It intentionally allows a tool that Magus does not expose; a
// guard may funnel an available capability, never remove one.
func rawToolDenied(c guardCommand) bool {
	_, ok := rawToolMatch(c)
	return ok
}

// rawToolMatch finds the operation whose rendered base command and semantic
// subcommand match c. Both the ordinary and rw renderings participate: a spell
// can expose a read-only check (`gofmt -l`) and a rewriting form (`gofmt -w`)
// without the guard confusing the two. The catalog is read for every process,
// so a newly registered spell operation requires no guard edit.
func rawToolMatch(c guardCommand) (guardToolMatch, bool) {
	for _, a := range c.Args {
		if a == "--version" || a == "-version" || a == "-V" {
			return guardToolMatch{}, false
		}
	}
	for _, spell := range project.DefaultSpellRegistry().All() {
		for _, operation := range spell.Targets() {
			for _, charms := range [][]string{nil, []string{"rw"}} {
				program, args, ok, err := spell.RenderCommand(operation, charms)
				if err != nil || !ok || program == "" || filepath.Base(program) != c.Name {
					continue
				}
				prefix := guardCommandPrefix(args)
				if len(prefix) == 0 || len(c.Args) < len(prefix) || !slices.Equal(c.Args[:len(prefix)], prefix) {
					continue
				}
				return guardToolMatch{spell: spell.Name(), operation: operation}, true
			}
		}
	}
	return guardToolMatch{}, false
}

// guardCommandPrefix extracts the semantic command portion from an operation's
// rendered argv. It preserves compound verbs (`go mod tidy`, `go tool
// govulncheck`) and uses write-mode flags as a verb when an operation has no
// subcommand (`gofmt -w`). Other leading flags describe a read-only rendering
// and therefore do not create a raw-tool deny.
func guardCommandPrefix(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			break
		}
		if arg == "-w" || arg == "--write" || arg == "--fix" {
			return []string{arg}
		}
	}
	first := 0
	for first < len(args) && strings.HasPrefix(args[first], "-") {
		first++
	}
	if first == len(args) || args[first] == "." || strings.HasPrefix(args[first], "./") {
		return nil
	}
	prefix := []string{args[first]}
	if (args[first] == "mod" || args[first] == "tool") && first+1 < len(args) && !strings.HasPrefix(args[first+1], "-") {
		prefix = append(prefix, args[first+1])
	}
	return prefix
}

// gitGuard classifies git invocations from PARSED commands, returning the first
// verdict any of them earns.
//
// Parsed rather than pattern-matched because the unanchored form denied `git
// stash` written as PROSE - a commit message, or the magus-vcs-hygiene skill,
// whose whole subject is those commands. A false positive here was once defended
// as the safe direction; with an AST it is not a trade at all, since a quoted
// word structurally cannot be a command, and `cd /repo && git stash` still
// matches however it is reached.
func gitGuard(cmds []guardCommand) (bashGuardVerdict, bool) {
	for _, c := range cmds {
		if c.Name != "git" || len(c.Args) == 0 {
			continue
		}
		sub, rest := c.Args[0], c.Args[1:]
		switch sub {
		case "stash":
			// Reading a stash is safe. RESTORING one is not, which this rule used to
			// assume it was: the stash stack is per-REPOSITORY, shared by every linked
			// worktree, so `git stash pop` with no ref applies whatever is at stash@{0} -
			// routinely a stranger's work from another worktree - into your tree, and
			// drops the entry if it applies cleanly. Naming the entry after reading
			// `git stash list` is the deliberate form and stays allowed.
			if len(rest) > 0 && slices.Contains([]string{"list", "show"}, rest[0]) {
				continue
			}
			if len(rest) > 1 && slices.Contains([]string{"pop", "apply", "drop", "branch"}, rest[0]) {
				continue // an explicit stash@{N}: the caller chose which entry
			}
			if len(rest) > 0 && slices.Contains([]string{"pop", "apply", "drop"}, rest[0]) {
				return bashGuardVerdict{Deny: denySharedStash(rest[0])}, true
			}
			return bashGuardVerdict{Deny: denyWholeTree("git stash")}, true
		case "worktree":
			if len(rest) > 0 && rest[0] == "remove" {
				return bashGuardVerdict{Deny: "git worktree remove deletes that worktree's working tree, including uncommitted and untracked work in it - which in a repo running several worktrees is routinely someone else's, and is not in any commit to recover from. Check it is clean first (git -C <path> status), and remove it from a session that owns it."}, true
			}
		case "reset":
			if slices.Contains(rest, "--hard") {
				return bashGuardVerdict{Deny: denyWholeTree("git reset --hard")}, true
			}
		case "checkout":
			if isWholeTreePathspec(rest) {
				return bashGuardVerdict{Deny: denyWholeTree("git checkout .")}, true
			}
		case "restore":
			if isWholeTreePathspec(rest) {
				return bashGuardVerdict{Deny: denyWholeTree("git restore .")}, true
			}
		case "clean":
			for _, a := range rest {
				if strings.HasPrefix(a, "-") && strings.ContainsAny(a, "fdxX") {
					return bashGuardVerdict{Deny: denyWholeTree("git clean")}, true
				}
			}
		}
	}

	// Advisories, in a second pass so a deny anywhere in a compound command wins
	// over an advisory earlier in it.
	for _, c := range cmds {
		if c.Name != "git" || len(c.Args) == 0 {
			continue
		}
		sub, rest := c.Args[0], c.Args[1:]
		switch sub {
		case "push":
			return bashGuardVerdict{Context: pushGuardContext}, true
		case "add":
			if slices.ContainsFunc(rest, func(a string) bool {
				return a == "-A" || a == "--all" || a == "-u" || a == "--update" || a == "."
			}) {
				return bashGuardVerdict{Deny: denyStageAll}, true
			}
			return bashGuardVerdict{Context: vcsGuardContext}, true
		case "commit":
			return bashGuardVerdict{Context: vcsGuardContext}, true
		case "checkout":
			// A revert needs the `--` separator; without it the operand is a
			// branch, which is not this rule's business.
			if slices.Contains(rest, "--") {
				return bashGuardVerdict{Context: revertGuardContext}, true
			}
		case "restore":
			// `git restore` targets worktree files by definition.
			return bashGuardVerdict{Context: revertGuardContext}, true
		}
	}
	return bashGuardVerdict{}, false
}

// gitGuardFallback applies the legacy regexes, and runs ONLY when the line does
// not parse. Its false positives on prose are the reason gitGuard exists, so it
// is confined to the case where there is no AST to consult - and there, an
// over-eager deny really is the safe direction, because these rules guard work
// that cannot be recovered.
func gitGuardFallback(command string) (bashGuardVerdict, bool) {
	switch {
	case guardStashRe.MatchString(command) && !guardStashSafeRe.MatchString(command):
		return bashGuardVerdict{Deny: denyWholeTree("git stash")}, true
	case guardResetRe.MatchString(command):
		return bashGuardVerdict{Deny: denyWholeTree("git reset --hard")}, true
	case guardCheckoutRe.MatchString(command):
		return bashGuardVerdict{Deny: denyWholeTree("git checkout .")}, true
	case guardRestoreRe.MatchString(command):
		return bashGuardVerdict{Deny: denyWholeTree("git restore .")}, true
	case guardCleanRe.MatchString(command):
		return bashGuardVerdict{Deny: denyWholeTree("git clean")}, true
	case guardPushRe.MatchString(command):
		return bashGuardVerdict{Context: pushGuardContext}, true
	case guardStageAllRe.MatchString(command):
		return bashGuardVerdict{Deny: denyStageAll}, true
	case guardStageRe.MatchString(command):
		return bashGuardVerdict{Context: vcsGuardContext}, true
	case guardScopedRevertRe.MatchString(command):
		return bashGuardVerdict{Context: revertGuardContext}, true
	}
	return bashGuardVerdict{}, false
}

// isWholeTreePathspec reports the `.` pathspec forms, with or without the `--`
// separator, and nothing narrower.
func isWholeTreePathspec(args []string) bool {
	for _, a := range args {
		if a == "--" {
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a == "."
	}
	return false
}

// The guard patterns. [^&|;]* keeps a flag search inside one segment of a
// compound command, so `git reset && tool --hard-mode` does not false-positive.
//
// These remain ONLY as the unparsable-line fallback: gitGuard above is the
// primary path, and it reads an AST instead of the raw text.
var (
	guardStashRe     = regexp.MustCompile(`\bgit\s+stash\b`)
	guardStashSafeRe = regexp.MustCompile(`\bgit\s+stash\s+(list|show|pop|apply|drop|branch)\b`)
	guardResetRe     = regexp.MustCompile(`\bgit\s+reset\b[^&|;]*--hard`)
	guardCheckoutRe  = regexp.MustCompile(`\bgit\s+checkout\s+(--\s+)?\.(\s|$)`)
	guardRestoreRe   = regexp.MustCompile(`\bgit\s+restore\b[^&|;]*\s\.(\s|$)`)
	guardCleanRe     = regexp.MustCompile(`\bgit\s+clean\b[^&|;]*\s-\w*[fdxX]`)
	guardStageRe     = regexp.MustCompile(`\bgit\s+(commit|add)\b`)
	// `git add -A` / `git add .` / `git add --all` / `git add -u`: stage-everything
	// forms. Split out from guardStageRe because these DENY - see evaluateBashGuard.
	guardStageAllRe = regexp.MustCompile(cmdPos + `git\s+add\s+(-A\b|--all\b|-u\b|--update\b|\.(\s|$))`)
	// Push, NOT commit. Committing in a half-finished state is ordinary and
	// sometimes necessary; a gate there would fire constantly and be tuned out.
	// Publishing is where the work stops being yours alone, so that is where the
	// reminder earns its place - and it stays an advise, because a push can
	// legitimately carry a work-in-progress branch.
	guardPushRe = regexp.MustCompile(`\bgit\s+push\b`)
	// A SCOPED revert: `git checkout -- <paths>` / `git restore <paths>`. The
	// whole-tree forms above already deny; this one is legitimate often enough
	// that it only advises, but it is the shape of the most common wrong reflex
	// an agent has about generated files.
	// `git checkout ... -- <paths>` needs the `--` separator to be a revert at
	// all; without it the argument is a branch (`git checkout main`, `-b foo`),
	// which is not this rule's business. `git restore` targets worktree files by
	// definition, so its bare form counts.
	guardScopedRevertRe = regexp.MustCompile(`\bgit\s+checkout\b[^&|;]*\s--\s|\bgit\s+restore\b`)
	// `cd <dir> && magus ...`: magus is CWD-relative, so this is the shape of
	// running the right command against the wrong project. Every magus command
	// that acts on a project takes it as an explicit argument, so the cd is
	// almost always avoidable - and when it is not (a DIFFERENT workspace), the
	// answer is --root, not a cd.
	guardCdMagusRe = regexp.MustCompile(`\bcd\s+\S+\s*(&&|;)\s*(\S*/)?magus\s`)

	// guardNotesWriteRe matches an invocation that would AUTHOR a note.
	//
	// The path rule already refuses an agent write into a notes store, but it only ever
	// sees file writes - and `magus notes edit` reading piped prose is a command, not a
	// file write, so it would sail past a boundary that is supposed to be about WHO is
	// writing rather than which surface they used. This closes that, so the rule holds
	// however the write is spelled.
	guardNotesWriteRe = regexp.MustCompile(`\bmagus\s+notes\s+edit\b`)

	// An IN-PLACE stream edit. Reading with sed is untouched; only -i is refused.
	//
	// The flag is not portable and the two spellings silently destroy each other's work:
	// GNU takes `sed -i 's/x/y/' f`, BSD/macOS reads that same line's `s/x/y/` as the
	// BACKUP SUFFIX and then has no script, while the portable `sed -i '' ...` makes GNU
	// treat `''` as the script and edit nothing. A command that works on the author's
	// machine mangles the file on the next one, and it does it by writing, so the damage is
	// already on disk when it is noticed.
	//
	// Every host driving this guard has a structured editor tool that reads the file,
	// applies an exact replacement, and reports what changed - which is the same operation
	// without the portability trap or the blind write.
	guardSedInPlaceRe = regexp.MustCompile(`\bsed\b[^|;&]*\s(-[a-zA-Z]*i[a-zA-Z]*\b|--in-place\b)`)

	// A repo-wide code search. This does NOT claim the agent asked the wrong
	// question - a hook cannot know that - only that a whole-tree text search has
	// a better tool here, because the graph answers from DECLARED sources while a
	// grep hit is a guess. Deliberately narrow: a recursive grep, a bare ripgrep
	// (effectively always repo-wide), or a find-by-name. A plain `grep pattern
	// file` is reading one file and is left alone.
	guardCodeSearchRe = regexp.MustCompile(`\bgrep\s+-[a-zA-Z]*[rR]|\brg\s|\bag\s|\bfind\s+\S+\s+-name\b`)

	// `magus ... && echo "TESTS GREEN"`. The exit status already carries that, which
	// is what an exit status is for; the echo adds a line that is true by
	// construction and tells a reader nothing the command did not.
	guardEchoOnSuccessRe = regexp.MustCompile(`(?:^|[;&|]\s*)(\S*/)?magus\s[^&|;]*&&\s*echo\b`)

	// A magus invocation whose own output is truncated or filtered by the shell.
	// magus has output flags for this; a pipe throws away the parts the agent
	// then has to guess at. jq is deliberately absent: it composes with -o json
	// rather than fighting it.
)

const (
	vcsGuardContext = "magus workspace: classify the dirty tree before staging or committing: magus describe file $(git diff --name-only). role=output paths are generated - never hand-edit them; regenerate and commit them with their source change. Stage the reviewed paths explicitly with `git add -- <paths>`. Load the magus-vcs-hygiene skill for the commit checklist if not already loaded."
	// An explicit ladder: naming the middle rung is the point, because a spell op
	// still runs through magus, so the cache, the sandbox and affected tracking
	// all survive. Rung 2 forwards args - `magus run go::go-test <p> -- -run
	// TestX`.
	//
	// DENIED, not advised: every tool matched here has an exact magus equivalent,
	// so the deny costs nothing, and an advisory loses to a trained reflex. As an
	// advisory it changed behaviour zero times over a long session and left the Go
	// build cache poisoned by uninstrumented raw runs.
	runGuardContext = "this has an exact equivalent in magus, so it is DENIED rather than explained - not because it is dangerous, but because the replacement does strictly more (cache, sandbox, affected tracking) and costs you nothing. If the command WRITES into the working tree (generate, a formatter with -w/--write/--fix, go mod tidy, build output on a tracked path) the rule is firm and has no exceptions: a raw write leaves the owning target reporting drift it did not cause, and the workspace's account of itself wrong. Escalate only as far as you actually need:\n" +
		"  1. TOP-LEVEL TARGET (use this almost always):  magus run test|build|lint|format|generate [<project>]  - `magus describe targets` lists every target, `-o name` for just the names\n" +
		"  2. ONE SPELL OP, still through magus, when a whole target is too broad:  magus run <spell>::<op> [<project>]  (e.g. magus run go::go-test libs/foo). `magus describe spell <name>` lists a spell's ops.\n" +
		"To see the exact command a target or op would run, WITHOUT running it, add --dry-run: `magus run go::go-test libs/foo --dry-run` prints `$ go test ./...`. Use that to learn what magus does under the hood instead of guessing and reaching for the raw tool.\n" +
		"Args after `--` are forwarded, so a specific flag is NOT a reason to reach for the raw tool: `magus run go::go-test libs/foo -- -run TestX` runs `go test ./... -run TestX`, and a magusfile target receives them as its `args: [str]` parameter. The operation already supplies its own default arguments: forward only the extra flags or overrides you need. Narrow by PROJECT too - `magus run test libs/foo` runs less. Load the magus-run skill if not already loaded.\n" +
		"Do NOT retry this behind a wrapper. The guard reads the command being RUN, not the one being typed, so a launcher, `env -u GOROOT ...`, a `VAR=value` prefix, and `bash -c '...'` all reach the same verdict. Run the named magus command directly."
	// Reverting regenerated output is the wrong default. An agent that did not
	// hand-edit a gen/ file concludes it is not "its" change and discards it -
	// but a generate target rewriting its declared outputs is the system working,
	// and those outputs belong in the same commit as the source that moved them.
	// The honest test is whether the SOURCE changed, not whether the agent typed
	// into the output.
	revertGuardContext = "magus workspace: do not revert a file just because you did not hand-edit it. Classify first: magus describe file <paths>. A role=output path is a declared target output, and if a source change moved it that is correct - it belongs in the SAME commit as the source, and reverting it is what makes CI fail on drift. Revert only when regenerating reproduces the same diff with the target's declared inputs unchanged, which means the drift is environmental (a tool version, a path baked into the output) rather than yours - report that instead of silently discarding it. Load the magus-vcs-hygiene skill if not already loaded."
	// ADVISE, not deny. Denying was tried and reverted: magus has no raw-text
	// search to fall back on, so "where does this string appear" has no magus
	// answer and the deny removed a capability. The advisory still applies the
	// pressure without making the agent unable to work.
	//
	// The reason must ROUTE, not scold: `magus query` indexes DOMAIN entities and
	// returns 0 for a code symbol, while `magus refs` indexes CODE symbols. An
	// agent that tries `magus query someFunc`, gets 0, and concludes the graph is
	// useless is the failure this text exists to prevent.
	// Names the mechanism, because the fix is not "remember where you are" - it is
	// that the project is an argument and never needs to be implied by the CWD.
	cwdGuardContext = "magus workspace: magus is CWD-relative, and `cd` before a magus command is how the right command lands on the wrong project. Pass the project explicitly instead - `magus run <target> <project>`, `magus describe project <path>`, `magus affected ci` - so the command means the same thing from anywhere. Project paths are workspace-relative and written bare (`libs/foo`), which is why they mean the same project from any directory. `magus where <name>` resolves a name to its path. Only a DIFFERENT workspace needs relocating, and that is `--root <path>`, not a cd."

	searchGuardReason = "this workspace has a knowledge graph, and a text match is a guess that misses generated, indirect, and cross-language references the graph knows about. Pick by what you are asking:\n" +
		"  CODE SYMBOL (where is it defined / used):  magus refs <symbol>   -> definition file, every referencing file, exact lines\n" +
		"  DOMAIN ENTITY (projects, targets, spells, ops, docs, diagnostics):  magus query \"<terms>\"  with kind:<k> project:<p> relation:<r> filters and -negation\n" +
		"  ONE node's edges, provenance, blast radius:  magus explain <node>\n" +
		"  HOW two things connect:  magus path <a> <b>\n" +
		"`magus query <symbol>` returns 0 for code symbols - that is refs's job, not query's. Every empty result carries a verdict saying which kind of empty it is: `absent` is a fact magus verified, `unknown` names what it could not search and how to fix it. Read the verdict rather than guessing.\n" +
		"If you are searching for raw TEXT rather than a symbol or an entity (a string literal, a comment, a config value), grep is the right tool and magus has no replacement - carry on. Load the magus-query skill for the full grammar."

	pushGuardContext = "magus workspace: `magus affected ci` is the gate before publishing - it runs the full pipeline over every project the diff reaches, including ones you never edited. Run it if you have not since your last change. If you are pushing deliberate work-in-progress, or you already ran it, push. Load the magus-run skill if not already loaded."

	// Named for what the agent should do instead, not for what it did wrong: the
	// exact safe replacement is the actionable part. `git add -A` is the single command
	// most likely to turn a focused change into an unreviewable one: it sweeps every
	// regenerated output and every unrelated formatting fix a target just wrote into
	// a commit about something else. Measured: one such call put 69 files - a whole
	// regenerated docs site plus five untouched source files - into a commit about
	// four collection methods.
	denyNotesAuthor = "authoring a note is denied because notes are human-authored by design - agents read them and never write them.\n" +
		"A note is the one thing in the knowledge graph that is not derived from the repository: nothing here corroborates it later, so its only provenance is the person who wrote it and signed the commit. That is why this is refused however the write is spelled - `magus notes edit` reading piped prose is a command rather than a file write, so the path rule would never have seen it.\n" +
		"Recording a DECISION ABOUT THIS WORKSPACE is what `magus memory put <name>` is for: the agent-writable store, where every entry cites a ref a later reader can re-run.\n" +
		"If the content genuinely belongs in the notes, say so and let the person run it themselves."

	denySedInPlace = "editing a file in place with sed is denied because the flag is not portable and the two spellings destroy each other's work.\n" +
		"GNU reads `sed -i 's/x/y/' f` as an edit; BSD and macOS read that same `s/x/y/` as the BACKUP SUFFIX and are then left with no script. The portable-looking `sed -i '' ...` inverts it: GNU takes `''` as the script and edits nothing. So the command that worked where it was written mangles the file on the next machine, and it does it by WRITING, so the damage is on disk before anyone reads the diff.\n" +
		"Use your editor tool instead - it reads the file, applies an exact replacement, and reports what changed. Reading with sed is untouched; only an in-place edit is refused.\n" +
		"For a whole-tree mechanical edit, `magus refs <symbol> --occurrences` gives column-precise sites to edit rather than a pattern that also matches the comment about it."

	denyStageAll = "staging everything is denied because it sweeps unrelated sources, generated outputs, and residue into one commit. First classify the dirty tree: `magus describe file $(git diff --name-only)`. Then stage only the reviewed source files and the generated outputs they require: `git add -- <paths>`.\n" +
		"Why this is not just style: a magus target writes its declared outputs as it runs, so a tree is routinely dirty with generated files you did not edit. `git add -A` commits them with no signal that it happened, and it also picks up build residue. Confirm the deliberate selection with `git diff --cached --stat` BEFORE committing. There is deliberately no `magus vcs` wrapper; load the magus-vcs-hygiene skill if not already loaded."

	// Both messages LEAD with the replacement, per this file's rule: the agent
	// reached for a filter because it wanted one specific thing, so the actionable
	// correction is the flag that returns that thing, not the prohibition.
	outputPipeDeny = "Ask magus for the field instead of filtering its output:\n" +
		"  -o name                      the ids/names, one per line\n" +
		"  -o json                      the full record\n" +
		"  -o template=<go-template>    one field, e.g. -o template='{{.Ref}}'\n" +
		"Denied rather than advised because a pipe also replaces the exit status with the last stage's, so a failing gate reads as exit 0.\n" +
		outputGuardTail
	outputRedirectDeny = "magus already wrote the log; you do not need to capture it:\n" +
		"  magus query output <ref>     the failing target's full captured log (this one may be redirected)\n" +
		"  .magus/logs/<hash>.log       the path, printed by the failure itself\n" +
		"  -o json --tee <file>         mirror structured output to a file (never console text)\n" +
		"Redirecting hides what you need next: --silent prints the diagnostics and the log path on failure, and `-s > /dev/null 2>&1` throws exactly that away.\n" +
		outputGuardTail
	throwawayCopyDeny = "This runs magus inside a temp or scratchpad copy, so its verdict describes a tree nobody ships: a green gate leaves the real tree unverified, generated files land in the copy, and the copy gets its own cache.\n" +
		"Run from the workspace and name the project: `magus run <target> <project>`. A different workspace is `--root <path>`; a pristine tree is a throwaway `git worktree`, not a copy."
	outputGuardTail = "The one exception is `magus query output <ref>`: a raw captured log has no schema to project, so searching it is a real need."

	// Advice, not a deny: it wastes a line, it does not break anything.
	echoOnSuccessAdvice = "The `&& echo ...` is redundant - the exit status already says the command passed, and a message that only prints on success carries no information the status did not. Drop it and read the status."
)

// denySharedStash explains why an unqualified stash restore is refused.
func denySharedStash(verb string) string {
	return "git stash " + verb + " with no entry named acts on stash@{0}, and the stash stack belongs to the REPOSITORY, not to your worktree - so the top entry is often work another checkout (or another agent) shelved, and " + verb + " applies or destroys it. Read `git stash list` first, then name the one you meant: git stash " + verb + " stash@{N}."
}

func denyWholeTree(op string) string {
	return "whole-tree " + op + " destroys uncommitted and untracked work, including a concurrent agent's. Verify builds in place (magus run build / magus affected ci); building never requires a clean tree. If you truly need a pristine tree, use a throwaway git worktree. See the magus-vcs-hygiene skill."
}

// evaluateBashGuard applies the guard rules in severity order.
//
// magus denies on three independent triggers and explains everything else:
//
//  1. it cannot be UNDONE - the whole-tree git rules;
//  2. it WRITES INTO THE WORKING TREE - codegen, formatters with -w/--fix,
//     dependency files, build output landing on a tracked path;
//  3. it has an EXACT WORKING EQUIVALENT - raw `go test` against `magus run test`.
//
// Trigger 2 is the firm one: reading through the wrong tool costs a cache hit,
// writing through it corrupts the workspace's account of itself. Trigger 3 is
// denied not because the command is dangerous but because the replacement is
// complete, which makes the deny free.
//
// A deny is only legitimate once the replacement it names actually works - the
// reverted grep deny removed a capability magus had nothing to route to. Do not
// add one without checking that path end to end.
func evaluateBashGuard(command string) bashGuardVerdict {
	// The program rules judge PARSED commands; the rest read the line as written,
	// because they are about its SHAPE - a pipe, a redirect, a cd before a magus
	// call - rather than which program runs.
	//
	// A matched git rule that only ADVISES is held, not returned: returning here
	// let a trailing `git commit` downgrade a deny to an advisory. Deny always
	// outranks advise, whichever rule saw the line first.
	// Authoring a note is refused before anything else, because it is the one rule whose
	// whole point is that it holds on EVERY surface: the path rule sees file writes, and a
	// note authored from piped prose is a command, so only this catches it.
	if guardNotesWriteRe.MatchString(command) {
		return bashGuardVerdict{Deny: denyNotesAuthor}
	}
	if guardSedInPlaceRe.MatchString(command) {
		return bashGuardVerdict{Deny: denySedInPlace}
	}
	var advisory bashGuardVerdict
	cmds, parsed := parseGuardCommands(command)
	if parsed {
		if v, matched := gitGuard(cmds); matched {
			if v.Deny != "" {
				return v
			}
			advisory = v
		}
	} else if v, matched := gitGuardFallback(command); matched {
		if v.Deny != "" {
			return v
		}
		advisory = v
	}

	rawToolCmd, rawToolDeny := firstRawToolDenied(command)
	switch {
	case rawToolDeny:
		match, _ := rawToolMatch(rawToolCmd)
		return bashGuardVerdict{Deny: explainDeny(command, rawToolCmd, runGuardContextFor(match))}
	case magusInThrowawayCopy(command):
		return bashGuardVerdict{Deny: throwawayCopyDeny}
	case magusPipedToFilter(command):
		return bashGuardVerdict{Deny: outputPipeDeny}
	case magusRedirected(command):
		return bashGuardVerdict{Deny: outputRedirectDeny}
	case guardCdMagusRe.MatchString(command):
		return bashGuardVerdict{Context: cwdGuardContext}
	case guardCodeSearchRe.MatchString(command):
		return bashGuardVerdict{Context: searchGuardReason}
	case guardEchoOnSuccessRe.MatchString(command):
		return bashGuardVerdict{Context: echoOnSuccessAdvice}
	}
	// Nothing denied, so a held git advisory is the answer after all.
	return advisory
}

func runGuardContextFor(match guardToolMatch) string {
	return fmt.Sprintf("Run this instead: `magus run %s::%s`. Tool flags and overrides remain available: `magus run %s::%s [<project>] -- <tool-args>`.\n\n%s", match.spell, match.operation, match.spell, match.operation, runGuardContext)
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
