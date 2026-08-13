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
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/json"
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
// `install` writes the embedded skills into explicitly named destinations and
// `sample` prints AGENTS.md content for a developer to own. Destinations and
// event shapes are explicit arguments, never auto-detected (per the
// explicit-and-granular preference); writing into a repo's agent-config dirs
// happens only through `install`, never as a side effect of another command.
//
// AGENTS.md is the one file magus refuses to write, and `install-agents-md`
// (which did) is gone with no subcommand replacing it. It managed a marked block
// inside the file, which is the polite version of an installer appending to your
// .bashrc and still the wrong shape: the file is the developer's, the merge logic
// is never as careful as it looks, and a re-run leaves bytes nobody wrote.
// `install` prints the block when your AGENTS.md is missing it or carrying a
// stale one, which is the moment the block is worth reading, and `sample` prints
// a whole starter file with the same block in it.
//
// The `hook` and `notify` subcommands used to live here; they are now top-level
// (`magus hook`, `magus notify`) because their contracts are not agent-specific.
// A guard evaluates any command or file path the host can produce, and a
// notification is whatever needs a human's attention regardless of source.
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
	fmt.Fprintln(w, "  --simple       install the shorter curated permutation of each skill:")
	fmt.Fprintln(w, "                 the same judgment with the enumeration dropped, for a")
	fmt.Fprintln(w, "                 reader that can re-derive the steps but not which")
	fmt.Fprintln(w, "                 failures are silent. Both permutations are hand-authored")
	fmt.Fprintln(w, "                 from one source and share one content digest, so they go")
	fmt.Fprintln(w, "                 stale together and `graph verify` treats them alike.")
	fmt.Fprintln(w, "                 Also writes an always-full <name>-full twin beside each")
	fmt.Fprintln(w, "                 skill: simple bets the installing reader can re-derive")
	fmt.Fprintln(w, "                 what it drops, and a delegated or smaller model never")
	fmt.Fprintln(w, "                 made that bet - point it at the twin by name.")
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
	dir := fset.String("dir", ".", "Repo directory to install into")
	force := fset.Bool("force", false, "Overwrite existing installed skill files (write mode)")
	simple := fset.Bool("simple", false, "Install the shorter curated permutation of each skill: the same judgment with the enumeration dropped, for a reader that can re-derive the steps but not which failures are silent. Also writes an always-full <name>-full twin beside each skill, for a delegated or smaller model to be pointed at by name")
	tarMode := fset.Bool("tar", false, "Stream a tar archive of the skills to stdout instead of writing files")
	global := fset.Bool("global", false, "Allow absolute destination paths in write mode (use --tar | tar -xf - for paths outside the repo instead)")
	fset.Usage = func() { agentUsage(os.Stderr) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	dests := fset.Args()

	if *tarMode {
		if len(dests) > 1 {
			return fmt.Errorf("agent install --tar: at most one destination path prefix is allowed (the path inside the tar archive)")
		}
		prefix := "."
		if len(dests) == 1 {
			prefix = dests[0]
		}
		body, err := agentSkills.SkillTar(prefix, agent.VariantOf(*simple))
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
	if !*global {
		for _, d := range dests {
			if filepath.IsAbs(d) || strings.HasPrefix(d, "~") {
				return fmt.Errorf("agent install: destination %q is outside the working tree; pass --global, or use --tar | tar -xf - -C %q instead", d, d)
			}
		}
	}

	var written []string
	for _, dest := range dests {
		w, err := agentSkills.WriteSkillTree(*dir, dest, *force, agent.VariantOf(*simple))
		if err != nil {
			return err
		}
		written = append(written, w...)
	}
	for _, p := range written {
		slog.InfoContext(ctx, "agent install: wrote", slog.String("path", p))
	}
	printAgentInstallNextSteps(*dir, written, agent.VariantOf(*simple))
	return nil
}

// printAgentInstallNextSteps prints an actionable hint after install, gated on
// the user-controlled hints preference so MAGUS_HINTS_ENABLED=false silences it.
func printAgentInstallNextSteps(dir string, written []string, v agent.Variant) {
	if !interactive.HintsEnabled() || len(written) == 0 {
		return
	}
	interactive.Emit(os.Stderr, fmt.Sprintf("installed %d file(s); commit them so your team and agents share them", len(written)))
	reportContextCost(dir, written, v)
	if v == agent.VariantSimple {
		interactive.Emit(os.Stderr, "each skill also has an always-full <name>-full twin: when you delegate to a smaller model, hand it that name - it never made the bet --simple makes about the reader")
	}
	// MAGUS.md is regenerated for HUMAN readers; the skills send agents to the live
	// verbs instead, because a generated index is only true as of its last run.
	interactive.Emit(os.Stderr, "regenerate MAGUS.md for human readers:  magus describe graph -o markdown  (the skills send agents to the live verbs - magus describe targets, magus ls)")
	interactive.Emit(os.Stderr, "safety: consider a line in your repo's agent instruction file so parallel agents cannot wipe each other's work:")
	interactive.Emit(os.Stderr, "  \""+vcsSafetyRule+"\"")
	interactive.Emit(os.Stderr, "starter AGENTS.md you can own and tweak (prints, never writes):  magus agent sample")
	printAgentsBlockToPaste(dir)
}

// printAgentsBlockToPaste offers the managed AGENTS.md block for the developer to
// paste, and says nothing when their file already carries a current one.
//
// This is what replaced `magus agent install-agents-md`, which WROTE the block
// into AGENTS.md. Magus does not edit a file the developer owns - for the same
// reason an installer appending to your .bashrc is bad manners, and it is the
// careful implementations that make the point, not the sloppy ones: the merge
// logic was marker-delimited and idempotent, and it still left bytes nobody
// wrote in a file nobody could easily audit.
//
// It rides on install rather than on a subcommand of its own because the block
// is only ever wanted right after the skills land, and a print-only subcommand
// would have been a second name for a thing `sample` already prints.
//
// Silent when the block is present and current, because 80 lines of Markdown
// emitted on every --force reinstall is how a reader learns to scroll past this
// command's output - including the parts that are actionable. CheckStatuses
// reads AGENTS.md to decide; reading it was never the objectionable part.
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

// agentSampleDoc returns a complete, opinionated-but-tweakable AGENTS.md starter a
// developer can paste and adapt. It is print-only (magus agent sample): magus hands
// over a whole file to own rather than managing one in place, so it never risks
// clobbering an AGENTS.md somebody wrote.
//
// The magus guidance arrives inside its begin/end markers, the same bytes install
// prints, so `magus graph verify` can grade it once pasted and a reader who only
// wants that part can lift it out on the markers. It carried an UNMARKED copy
// before this file became the sole source of the block, which left a paste from
// here invisible to verification.
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

// agentSampleCmd prints agentSampleDoc to stdout. It never writes a file: an
// AGENTS.md is the developer's to own, and clobbering an existing one would be the
// opposite of helpful.
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

// hookCmd implements `magus hook`: evaluate one shell command against the guard
// rules and emit a verdict. It reads exactly one command or file path from
// stdin. The caller owns any extraction from its host-specific event shape;
// magus owns only the host-neutral policy and response. The verdict goes out
// through the standard -o arm, so a caller-specific response shape is a
// documented template, not code. A guard must fail open: an empty or unreadable
// input is a pass, never an error that would block every tool call.
//
// This used to be `magus agent hook`; it moved to top level because the guard
// rules are not agent-specific. Any caller that can produce a command or a file
// path on stdin can wire it.
func hookCmd(ctx context.Context, in io.Reader, out io.Writer, args []string) error {
	fset := flag.NewFlagSet("hook", flag.ContinueOnError)
	asPath := fset.Bool("path", false, "Judge the input as a FILE PATH an edit is about to write, not as a shell command: editing a declared target output is advised against")
	// Attribution, not policy: these name WHO produced the observation, and the
	// guard's verdict never reads them. Every one is optional and unvalidated -
	// including the host name, which is an opaque label the caller chooses rather
	// than a set magus knows, because a magus that enumerated hosts would need a
	// release per host. A wrapper that cannot extract a session id must still get
	// a verdict; erroring here would block a tool call over metadata.
	host := fset.String("agent-name", "", "Name of the agent host this invocation came from, recorded on the activity event")
	session := fset.String("session", "", "The host's own session id for this invocation, recorded on the activity event")
	event := fset.String("event", "", "The host's hook event name (e.g. PreToolUse), recorded on the activity event")
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
	who := hookAttribution{Host: *host, Session: *session, Event: *event}
	// A host that writes its hook payload as JSON needs no jq and no --path: the envelope
	// says what is about to run and whether it is a write. Explicit flags still win, since
	// a wrapper that passed them meant them.
	if req, isEnvelope := decodeHookEnvelope(input.Value); isEnvelope {
		input.Value = req.Value
		if req.IsPath {
			*asPath = true
		}
		if who.Session == "" {
			who.Session = req.Who.Session
		}
		if who.Event == "" {
			who.Event = req.Who.Event
		}
	}
	verdict := guardVerdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "pass"}
	if *asPath {
		if hasInput {
			// The generated-output rule is definitive (it reads declared globs), so it
			// speaks first; the memory nudge is a heuristic on the filename and only
			// fills the silence it leaves.
			context := adviseGeneratedWrite(ctx, input.Value)
			if context == "" {
				context = adviseInstalledSkillWrite(input.Value)
			}
			if context == "" {
				context = adviseMemoryWrite(input.Value)
			}
			if context != "" {
				verdict.Decision = "advise"
				verdict.Context = context
			}
		}
		appendHookActivity(ctx, input, who, true, verdict)
		if err := writeGuardVerdict(out, opts, verdict); err != nil {
			return err
		}
		return enforceVerdict(verdict)
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
	return enforceVerdict(verdict)
}

// guardDenyExitCode is what a denied command exits with.
//
// A hook that reports a deny and then exits 0 blocks NOTHING: the host reads the status,
// sees success, and runs the command anyway - so the guard looks installed, the rules look
// enforced, and neither is true. That is worse than leaving it unwired, because it is
// believed. Every rule in this guard was reachable and correct while exiting 0, which is
// exactly how it went unnoticed.
//
// 2 rather than 1 because it is what the dominant host reads as "block and show the reason
// to the model"; hosts that only test for non-zero block on it just the same. It collides
// with the usage exit code, and that is harmless: a guard that could not parse its input
// has not judged the command either, so refusing to run it is the same right answer.
const guardDenyExitCode = 2

// enforceVerdict turns a deny into a blocking exit, with the reason on STDERR where a host
// forwards it to the model. Stdout still carries the rendered verdict, in whatever format
// was asked for, so a structured consumer reads the same answer it always did - the exit
// code is added information, not a replacement. It applies to EVERY format: a deny is a
// deny however it is rendered, and an `-o json` caller that got a zero status would be
// told the same lie in a different shape.
func enforceVerdict(verdict guardVerdict) error {
	if verdict.Decision != "deny" {
		return nil
	}
	fmt.Fprintln(os.Stderr, verdict.Reason)
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
// there is nothing to say. Unlike the command rules this is not a heuristic:
// magus knows every target's declared outputs, so a role=output path is
// generated by definition and an edit to it will be overwritten by the next run.
//
// It TEACHES rather than blocks, which is the rule the whole guard follows:
// magus denies only what cannot be undone, and explains everything else. A
// hand-edited generated file is wasteful, not destructive - regenerating erases
// it - so it fails the cannot-be-undone test the whole-tree VCS operations pass.
// Blocking would also treat the agent as unable to learn, when the
// classification it needs is one `magus describe file` away.
//
// Silent on every uncertainty - no workspace, an unreadable one, an unclaimed
// path - because an advisory fired on a guess trains the reader to ignore it.
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
	return fmt.Sprintf("magus workspace: %s is a DECLARED OUTPUT of project %s - it is generated, and the next run of its producing target overwrites whatever you write there. This is not a style rule: magus reads the target's declared output globs, so the classification is definitive. Change the SOURCE that produces it instead, then run `magus run generate %s` (or the producing target) and commit the regenerated file together with your source change. `magus describe file %s` classifies any path. Load the magus-vcs skill if not already loaded.", f.Path, owner, owner, f.Path)
}

// adviseMemoryWrite nudges a magus-domain decision toward `magus memory put`
// when the write lands in one of the cross-host instruction files, or "" for
// every other path.
//
// CAPTURE, not replication: it does not argue against writing the file. Host
// instructions belong there. What it says is that a decision ABOUT THE
// WORKSPACE outlives the file it is being written into - the file is per-host
// and per-checkout, while a memory entry survives the worktree, the session,
// and a change of agent host. Naming both destinations is the point; an
// advisory that only said "do not write here" would be answering a question
// nobody asked.
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
	return "magus workspace: this is a per-host instruction file - it lives in one checkout and one host's conventions, and a second worktree or a different agent host does not see it. If what you are recording is a DECISION ABOUT THIS WORKSPACE (a target, a saved query, an output ref, a doc), put it in the handoff journal too: `magus memory put <name>` keeps it outside the checkout, where it survives worktrees, sessions, and hosts. Host instructions are right where they are; workspace decisions are not. Load the magus-memory skill if not already loaded."
}

// adviseInstalledSkillWrite explains that an installed skill is generated, or
// "" when the path is not one.
//
// The other two path advisories catch a write that is wasted; this one catches
// a write that DISAPPEARS. An installed skill is rendered from magus's embedded
// sources and stamped, so an edit to it is reported as drift by `graph verify`
// and erased by the next install --force. Neither event explains itself at the
// moment the edit is made, which is the only moment the advice is useful.
//
// The stamp is the discriminator, not the path: a workspace's own skill sits in
// the same directory under a name magus does not ship, and telling an author
// their own file is generated would be worse than saying nothing. So this reads
// the file and speaks only for one magus wrote - and stays silent on an
// unreadable one, like every other advisory here, because a nudge fired on a
// guess trains the reader to ignore the ones that are right.
//
// It is unreachable in magus's OWN tree, which is worth knowing before hunting
// for a bug: this repository declares its installed skills as outputs of the
// root generate target, so adviseGeneratedWrite claims the path first and says
// something more specific (it can name the producing target). Nobody else does
// that - a workspace installs skills with `agent install`, which is not a
// target and declares nothing - so in every repo but this one, this advisory is
// the only thing that speaks.
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
	return "magus workspace: that file is an INSTALLED skill - magus generates it from its own embedded sources and stamps it with a content digest. Editing it does not fail loudly, it fails silently in two ways: `magus graph verify` reports the file as stale rather than reading what you wrote, and the next `magus agent install <dir> --force` overwrites it. Rules that belong to THIS workspace go in a local skill beside the installed ones instead - a directory magus does not ship, conventionally magus-local-development, which install and verify both leave alone by construction. Stamp each rule with its evidence and the condition that retires it. Load the magus-adapt skill for the format and the rest of the method."
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
	ToolInput     struct {
		Command  string `json:"command"`
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
}

// decodeHookEnvelope pulls the thing to judge out of a host's hook payload, reporting
// whether the input was an envelope at all.
//
// Without this, wiring the guard into a host means `jq -r .tool_input.command | magus
// hook` - an extra dependency in the one place that must never fail, sitting on the
// critical path of every tool call. Reading the envelope directly makes the config a
// single command, and lets the attribution (session, event) come from the payload that
// already carries it instead of from flags the wrapper has to remember.
//
// A payload with a file_path rather than a command is a WRITE, which is the --path
// question, so the envelope decides that too: a caller that pipes real JSON should not
// also have to know which flag its shape implies.
//
// Anything that is not an object with a usable tool_input is left alone and judged as the
// literal text it is - the bare-command form keeps working exactly as before.
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
	default:
		return hookRequest{}, false
	}
	return req, true
}

// hookRequest is what a host's payload asked the guard to judge: the text, whether it is a
// path rather than a command, and who reported it.
type hookRequest struct {
	Value  string
	IsPath bool
	Who    hookAttribution
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

// cmdPos anchors a pattern to a COMMAND position: the start of the line, or just
// after a shell separator. Without it a pattern matches its own name appearing as
// text, which only became load-bearing once these verdicts turned into denials -
// `go test` and `git add -A` show up constantly in test data, documentation, and
// commit messages, where `git reset --hard` almost never did. Writing the guard's
// own test file through a shell heredoc was itself denied before this existed.
//
// Deliberately NOT applied to the whole-tree VCS patterns: those deny work that
// cannot be recovered, so a rare false positive there is the safe direction, and
// `cd /repo && git stash` must keep matching however it is reached.
// A separator preceded by a BACKSLASH is not a shell separator - it is an escape
// inside a quoted argument, and the commonest case is a grep alternation
// (`grep "golangci-lint\|mockery"`). RE2 has no lookbehind, so the char before the
// separator is consumed by a negated class instead. `^` keeps the start-of-string
// case, where there is no preceding char to inspect.
//
// Found by dogfooding: this pattern denied `grep -n "golangci-lint\|mockery|gofmt"`,
// reading the regex alternation as a pipe into `mockery`.
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
// This is a PARSER and not a pattern for one reason: every wrong verdict this
// guard has produced was a tokenizing mistake, not a policy mistake. A regex
// cannot tell a pipe from a pipe inside quotes, so a grep whose pattern
// contained an alternation denied as a pipe into gofmt. It cannot tell an
// assignment prefix from an argument, so a quoted GOFLAGS value stranded the
// payload off the anchor. It cannot see into a shell's own -c argument. And it
// has to reconstruct wrapper peeling by substitution, which is where the
// backtick-in-a-comment false positive came from.
//
// An AST answers all four structurally rather than by accumulated dogfooding:
// Assigns is a separate field from Args, a quoted separator is a Lit inside
// DblQuoted and cannot be a BinaryCmd, and a -c payload is just a string to
// parse again.
//
// The bool is false when the line does not parse, and the caller skips the
// raw-tool rules rather than guessing. That is less of a bypass than it looks:
// shell that does not parse does not run either. The residual is a construct
// valid in the caller's shell but not in this parser's bash dialect.
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
// shell text filter.
//
// DENIED rather than advised, and the reason is a measurement taken on this very
// session: as an advisory it fired repeatedly while the author of the rule piped
// `magus query output <ref>` into grep anyway, several times, reading straight
// past its own reminder. That is the same trained-reflex result the raw-tool
// advisory produced, so it gets the same answer.
//
// `magus query output <ref>` is the ONE exemption. Every other verb emits a
// structured record that `-o json`, `-o name`, or `-o template=` will shape
// exactly, so a filter there is pure loss. `query output` returns a target's raw
// captured log - arbitrary tool text with no schema for magus to project - and
// searching a build log is a real need with no flag that replaces it.
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

// magusRedirected reports a magus command whose stdout or stderr is being sent to
// a file, to /dev/null, or folded together with 2>&1.
//
// Denied for the same reason as a pipe, and measured the same way: a redirect
// discards the part you then have to guess at. `--silent > /dev/null 2>&1` is the
// worst of them - silent mode's whole contract is that it stays quiet UNTIL
// something fails, at which point it prints the likely diagnostics and the
// full-log path, and the redirect throws away exactly that. Observed in one
// session: three gate runs sent to /dev/null, each reporting only an exit code,
// each requiring a re-run to learn the cause.
//
// `magus query output <ref>` is exempt with the pipe rule's reasoning: it emits a
// raw captured tool log with no schema to project. Note that --tee is NOT the
// console-output escape hatch a reader might assume - it mirrors STRUCTURED output
// only (-o json|yaml|jsonl|template) - so the redirect message points at the log
// magus already persisted rather than at a flag that would silently write nothing.
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
// This is denied rather than advised because of what it produces: a verdict about
// a tree nobody will ship. A gate that passes in a stale duplicate is worse than
// no gate - the real tree stays unverified while reading as green. It also splits
// the cache (a second .magus alongside the real one), strands every generated file
// the run writes inside the copy, and duplicates spell sources, which is its own
// diagnostic (MGS1002).
//
// The observed shape is a whole pipeline chained onto it:
//
//	SP=/private/tmp/.../scratchpad; cd "$SP/fixci" && go test ./std/ 2>&1 | tail -3 && ./magus run generate:rw . -s >/dev/null 2>&1
//
// so the assignment is resolved too - keying only on a literal `cd /tmp/...`
// would miss the form that actually gets written.
//
// A genuinely different workspace is not this: that is `--root <path>`, which
// says so explicitly and keeps one cache.
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
// It returns the command rather than a bool so the verdict can name it. A guard
// that says only "denied" teaches nothing, and the agent's next move is to guess
// - which is how a denial becomes a wrapper hunt instead of a correction. This
// matters most for exactly the cases the parser exists to catch: told that
// `bash -c '...'` was denied, a reader has to work out which part offended,
// whereas "the command it resolves to is: go test ./..." is self-evident.
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
// These rules were the last unanchored regexes in the guard, and they cost the
// same way the raw-tool ones did: `git stash` written as PROSE denied. Writing
// the magus-vcs skill - the document whose whole subject is those commands -
// through a shell heredoc was blocked twice in one session, and a commit message
// mentioning the command would be too. The old comment argued a false positive
// here is "the safe direction" because the denials protect unrecoverable work.
// That trade was real when a regex was the only tool; with an AST it is not a
// trade at all, because a quoted word structurally cannot be a command. The
// safety property that mattered - `cd /repo && git stash` matching however it is
// reached - is strictly better served by parsing, which sees both commands.
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

	// A repo-wide code search. This does NOT claim the agent asked the wrong
	// question - a hook cannot know that - only that a whole-tree text search has
	// a better tool here, because the graph answers from DECLARED sources while a
	// grep hit is a guess. Deliberately narrow: a recursive grep, a bare ripgrep
	// (effectively always repo-wide), or a find-by-name. A plain `grep pattern
	// file` is reading one file and is left alone.
	guardCodeSearchRe = regexp.MustCompile(`\bgrep\s+-[a-zA-Z]*[rR]|\brg\s|\bag\s|\bfind\s+\S+\s+-name\b`)

	// A magus invocation whose own output is truncated or filtered by the shell.
	// magus has output flags for this; a pipe throws away the parts the agent
	// then has to guess at. jq is deliberately absent: it composes with -o json
	// rather than fighting it.
	//
	// The magus-is-the-COMMAND anchoring this block used to need lives in the
	// parser now: magusPipedToFilter and magusRedirected resolve the actual
	// command, which is why the old regexp - and the `grep x cmd/magus/... | head`
	// false positive it was written to dodge - are both gone.
)

const (
	vcsGuardContext = "magus workspace: classify the dirty tree before staging or committing: magus describe file $(git diff --name-only). role=output paths are generated - never hand-edit them; regenerate and commit them with their source change. Stage the reviewed paths explicitly with `git add -- <paths>`. Load the magus-vcs skill for the commit checklist if not already loaded."
	// An explicit ladder, because the old text ended with "if no target covers
	// this work, proceed" - which reads as permission to go straight to the raw
	// binary. There is a rung between the two, and naming it is the whole point:
	// a spell op still runs through magus, so the cache, the sandbox, and
	// affected tracking all survive.
	//
	// Rung 2 DOES forward args: `magus run go::go-test <p> -- -run TestX` runs
	// `go test ./... -run TestX`. That looked broken until the cache keyed extra
	// args - the run replayed a cached success, so the arg never executed and the
	// feature appeared missing.
	// DENIED, not advised: every tool matched here has an exact magus equivalent,
	// so the deny costs nothing. It WAS an advisory, and an advisory loses to a
	// trained reflex - `go test ./...` is muscle memory in a way `git reset --hard`
	// never is. Measured: a long session with this advisory firing on every raw `go`
	// call changed behaviour zero times, and left the Go build cache poisoned
	// (uninstrumented raw runs against magus's coverage-instrumented ones) into a
	// link-time fingerprint mismatch that took a full `go clean -cache` to clear.
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
	revertGuardContext = "magus workspace: do not revert a file just because you did not hand-edit it. Classify first: magus describe file <paths>. A role=output path is a declared target output, and if a source change moved it that is correct - it belongs in the SAME commit as the source, and reverting it is what makes CI fail on drift. Revert only when regenerating reproduces the same diff with the target's declared inputs unchanged, which means the drift is environmental (a tool version, a path baked into the output) rather than yours - report that instead of silently discarding it. Load the magus-vcs skill if not already loaded."
	// ADVISE, not deny. Denying was tried and reverted: magus has no raw-text
	// search to fall back on, verified against a built binary - `magus query`
	// fuzzy-matches the DOMAIN graph (targets, Buzz functions, docs) and returns
	// 0 for a host-language symbol, `magus refs` needs the exact symbol name, and
	// `magus x` is an interactive TTY picker. So "where does this string appear"
	// has no magus answer, and denying grep removed a capability with no
	// replacement - it blocked three legitimate lookups in one session. The
	// advisory still fires on every repo-wide search, which is the pressure that
	// matters, without making the agent unable to work.
	//
	// The reason must ROUTE, not scold. The two surfaces answer different
	// questions and confusing them is why the graph gets abandoned: `magus query`
	// indexes DOMAIN entities (projects, targets, spells, ops, docs) and returns 0
	// for a code symbol, while `magus refs` indexes CODE symbols. An agent that
	// tries `magus query someFunc`, gets 0, and concludes the graph is useless is
	// the failure this text exists to prevent - so it names the prerequisite index
	// too, since refs is empty until one is built.
	// Names the mechanism, because the fix is not "remember where you are" - it is
	// that the project is an argument and never needs to be implied by the CWD.
	cwdGuardContext = "magus workspace: magus is CWD-relative, and `cd` before a magus command is how the right command lands on the wrong project. Pass the project explicitly instead - `magus run <target> <project>`, `magus describe project <path>`, `magus affected ci` - so the command means the same thing from anywhere. Project paths are workspace-relative (`libs/foo`, or `workspace://libs/foo`; both parse). `magus where <name>` resolves a name to its path. Only a DIFFERENT workspace needs relocating, and that is `--root <path>`, not a cd."

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
	denyStageAll = "staging everything is denied because it sweeps unrelated sources, generated outputs, and residue into one commit. First classify the dirty tree: `magus describe file $(git diff --name-only)`. Then stage only the reviewed source files and the generated outputs they require: `git add -- <paths>`.\n" +
		"Why this is not just style: a magus target writes its declared outputs as it runs, so a tree is routinely dirty with generated files you did not edit. `git add -A` commits them with no signal that it happened, and it also picks up build residue. Confirm the deliberate selection with `git diff --cached --stat` BEFORE committing. There is deliberately no `magus vcs` wrapper; load the magus-vcs skill if not already loaded."

	// Both messages LEAD with the replacement, per this file's rule: the agent
	// reached for a filter because it wanted one specific thing, so the actionable
	// correction is the flag that returns that thing, not the prohibition.
	outputPipeDeny = "ASK MAGUS FOR THE FIELD INSTEAD OF FILTERING ITS OUTPUT. You piped into a text filter to pull out one value; magus already projects exactly that:\n" +
		"  -o name                      just the ids/names, one per line - what grep/awk/cut were being used to recover\n" +
		"  -o json                      the full structured record\n" +
		"  -o template=<go-template>    one precise field, e.g. -o template='{{.Ref}}'\n" +
		"A pipe is denied rather than advised because it also REPLACES the exit status with the last stage's: `magus affected ci | tail` reports tail's success, so a failing gate reads as exit 0 and the failure is silently lost. Combining a filter with -s/--silent is not the careful version - silent already bounds the output, so there is nothing left to trim.\n" +
		outputGuardTail
	outputRedirectDeny = "MAGUS ALREADY WROTE THE LOG - you do not need to capture it. Every run persists its full output, and a failure prints that path plus the ref:\n" +
		"  magus query output <ref>     the failing target's complete captured log (this one MAY be redirected)\n" +
		"  .magus/logs/<hash>.log       the full log path, named in the failure output itself\n" +
		"  -o json --tee <file>         mirror STRUCTURED output to a file (--tee only writes -o json|yaml|jsonl|template, never console text)\n" +
		"Redirecting is denied because it hides the one thing you need next. -s/--silent stays quiet UNTIL something fails, then prints the likely diagnostics plus that log path - so `-s > /dev/null 2>&1` throws away precisely what silent mode exists to print, leaving an exit code and a re-run. `2>&1` is never needed: magus already writes diagnostics where you are reading.\n" +
		outputGuardTail
	throwawayCopyDeny = "RUN MAGUS IN THE REAL WORKSPACE, NOT A COPY OF IT. This `cd`s into a temp or scratchpad directory and runs magus there, so whatever it reports describes a tree nobody will ship:\n" +
		"  - a gate that passes in a stale duplicate leaves the real tree unverified while reading as green\n" +
		"  - every file the run generates lands in the copy and is lost\n" +
		"  - the copy gets its own .magus cache, so nothing is shared and duplicated spell sources trip MGS1002\n" +
		"Run the command from the workspace itself, and name the project rather than moving: `magus run <target> <project>`. If you genuinely mean a DIFFERENT workspace, say so explicitly with `--root <path>` - that keeps one cache and one account of what was verified. To compare against a pristine tree, use a throwaway `git worktree`, which is a real checkout rather than a copy."
	outputGuardTail = "The one command you MAY pipe or redirect is `magus query output <ref>`: it returns a target's raw captured tool log, which has no schema for magus to project, so searching it is a real need. Every other verb emits a structured record that -o already shapes exactly."
)

// denySharedStash explains why an unqualified stash restore is refused.
func denySharedStash(verb string) string {
	return "git stash " + verb + " with no entry named acts on stash@{0}, and the stash stack belongs to the REPOSITORY, not to your worktree - so the top entry is often work another checkout (or another agent) shelved, and " + verb + " applies or destroys it. Read `git stash list` first, then name the one you meant: git stash " + verb + " stash@{N}."
}

func denyWholeTree(op string) string {
	return "whole-tree " + op + " destroys uncommitted and untracked work, including a concurrent agent's. Verify builds in place (magus run build / magus affected ci); building never requires a clean tree. If you truly need a pristine tree, use a throwaway git worktree. See the magus-vcs skill."
}

// evaluateBashGuard applies the guard rules in severity order.
//
// magus denies on three independent triggers, and explains everything else:
//
//  1. it cannot be UNDONE - the whole-tree git rules;
//  2. it WRITES INTO THE WORKING TREE - codegen, formatters with -w/--write/--fix,
//     dependency files, build output landing on a tracked path;
//  3. it has an EXACT WORKING EQUIVALENT - a raw `go test` against `magus run test`.
//
// Trigger 2 is the firm one, and the only one with no judgement in it. A write
// that skips magus is not merely slower: the target that owns that path now
// reports drift it did not cause, the cache holds a result for a tree that no
// longer exists, and affected tracking has no record that anything moved. Reading
// through the wrong tool costs a cache hit; WRITING through the wrong tool
// corrupts the workspace's account of itself. So the reflex to encode is
// asymmetric on purpose: a raw linter is a missed opportunity, a raw formatter is
// a defect.
//
// Trigger 3 is the one that needs the justification: a raw `go test` is harmless
// and reversible, so it fails the first two tests entirely. It is denied because
// the replacement is complete, which makes the deny free.
//
// That distinction is what the reverted repo-wide-search deny got wrong. Denying
// grep was not a mistake because grep is safe; it was a mistake because magus has
// no raw-text search to route to, so the deny removed a capability. Where the
// equivalent exists and works, a deny costs nothing - and an advisory costs
// everything, because a trained reflex reads straight past it. Measured, not
// assumed: a session that had this advisory fire on every raw `go` invocation
// changed its behaviour zero times, and left a poisoned Go build cache
// (uninstrumented raw runs against magus's coverage-instrumented ones) that
// surfaced as a link-time fingerprint mismatch.
//
// A deny is only legitimate once the replacement it names actually works. Do not
// add one here without checking that path end to end.
func evaluateBashGuard(command string) bashGuardVerdict {
	// The program rules judge PARSED commands; the rest read the line as written,
	// because they are about the SHAPE of the line - a pipe, a redirect, a cd
	// before a magus call - rather than about which program runs.
	// A matched git rule that only ADVISES is held, not returned: returning it here
	// let a trailing `git commit` downgrade a deny to an advisory, because this ran
	// before the rules below. Observed on a real command that cd'd into a scratchpad
	// copy, redirected four magus runs to /dev/null, and ended with `git commit` -
	// the git advisory answered and the two denials never got to speak. Deny always
	// outranks advise, whichever rule saw the line first.
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
// BYTES, not tokens, and deliberately. A token count is only true for one
// tokenizer, and these files are installed for whatever agent host the reader
// uses; publishing a number that is wrong for most of them is worse than
// publishing the one number that is right for all of them. Bytes are also the
// only figure magus can compute without shipping a tokenizer it would then have
// to keep matched to somebody else's model.
//
// The point of printing it at all is accountability. Skills are always-loaded
// instruction text: every byte here is a byte the reader does not get to spend
// on their own problem, and a surface that never states its own cost has no
// pressure on it to shrink.
func reportContextCost(dir string, written []string, v agent.Variant) {
	// Twins are excluded from BOTH sides of the comparison, and they have to be:
	// the alternative is VariantSize, which reports primaries only, so counting
	// the twins a simple install writes would compare 26 files against 13 and
	// report simple as nearly double the cost of full - the exact inverse of
	// what --simple does to the always-loaded set. A twin is a reference copy
	// fetched by name when a delegate needs it, not text every session carries.
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
	other := agent.VariantFull
	label := "the default form"
	if v == agent.VariantFull {
		other, label = agent.VariantSimple, "--simple"
	}
	msg := fmt.Sprintf("context cost: %s of always-loaded instructions (%s form)", byteSize(installed), v)
	if alt, err := agentSkills.VariantSize(other); err == nil && alt > 0 {
		msg += fmt.Sprintf("; %s would be %s", label, byteSize(alt))
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
