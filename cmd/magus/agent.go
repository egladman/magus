package main

import (
	"bytes"
	"context"
	"embed"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/egladman/magus/internal/agent"
	json "github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/types"
)

// skillFS holds provider-neutral skill bodies embedded at build time.
//
//go:embed skills
var skillFS embed.FS

// agentsSection is the distilled always-on block installed into AGENTS.md for
// platforms that read that contract instead of skill directories (Codex, Aider,
// and most other AGENTS.md-reading agents). Same rules as the skills, compressed.
//
//go:embed agents-section.md
var agentsSection string

// agentSkills binds the command's embedded source assets to the reusable
// provider-neutral catalog. The CLI owns embedding and presentation; internal/agent
// owns the artifact's rendering, provenance, installation, and verification.
var agentSkills = agent.NewCatalog(skillFS, agentsSection, types.KnowledgeSchemaVersion)

// agentCmd implements `magus agent <subcommand>`: the agent-integration surface.
// `install` writes the embedded skills into explicitly named destinations,
// `install-agents-md` maintains the managed magus section in AGENTS.md, and
// `hook` evaluates one shell command to a guard verdict. Destinations and
// event shapes are explicit arguments, never auto-detected (per the
// explicit-and-granular preference); writing into a repo's agent-config dirs
// happens only through these commands, never as a side effect of another.
func agentCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return agentUsageErr()
	}
	switch args[0] {
	case "install":
		return agentInstallCmd(ctx, args[1:])
	case "install-agents-md":
		return agentInstallAgentsMDCmd(ctx, args[1:])
	case "sample":
		return agentSampleCmd()
	case "hook":
		return agentHookCmd(ctx, os.Stdin, os.Stdout, args[1:])
	case "-h", "--help", "help":
		agentUsage(os.Stderr)
		return nil
	default:
		return fmt.Errorf("agent: unknown subcommand %q (try: install, install-agents-md, sample, hook)", args[0])
	}
}

func agentUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus agent <install|install-agents-md|sample|hook> [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  install            render the embedded skills and write or stream them")
	fmt.Fprintln(w, "                     into named destinations (.claude/skills, .agents/skills,")
	fmt.Fprintln(w, "                     .opencode/skills, ...)")
	fmt.Fprintln(w, "  install-agents-md  maintain the managed magus section in AGENTS.md")
	fmt.Fprintln(w, "                     (created if absent, replaced in place on re-install,")
	fmt.Fprintln(w, "                     bytes outside the markers never touched)")
	fmt.Fprintln(w, "  sample             print a starter AGENTS.md to stdout to own and tweak;")
	fmt.Fprintln(w, "                     never writes a file")
	fmt.Fprintln(w, "  hook               evaluate one shell command against the magus guard")
	fmt.Fprintln(w, "                     rules and emit a deny/advise/pass verdict")
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
	dir := fset.String("dir", ".", "Repo directory to install into")
	force := fset.Bool("force", false, "Overwrite existing installed skill files (write mode)")
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
		body, err := agentSkills.SkillTar(prefix)
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
		w, err := agentSkills.WriteSkillTree(*dir, dest, *force)
		if err != nil {
			return err
		}
		written = append(written, w...)
	}
	for _, p := range written {
		slog.InfoContext(ctx, "agent install: wrote", slog.String("path", p))
	}
	printAgentInstallNextSteps(written)
	return nil
}

// agentInstallAgentsMDCmd renders the managed magus section for <dir>/AGENTS.md
// and either writes it back in place or streams it on stdout (--tar).
// Bytes outside the begin/end markers are preserved from any existing file.
func agentInstallAgentsMDCmd(ctx context.Context, args []string) error {
	fset := flag.NewFlagSet("agent install-agents-md", flag.ContinueOnError)
	dir := fset.String("dir", ".", "Repo directory whose AGENTS.md to manage")
	tarMode := fset.Bool("tar", false, "Stream a tar archive containing AGENTS.md to stdout instead of writing")
	fset.Usage = func() { agentUsage(os.Stderr) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if rest := fset.Args(); len(rest) > 0 {
		return fmt.Errorf("agent install-agents-md: takes no positional arguments")
	}
	if *tarMode {
		body, err := agentSkills.AgentsSectionTar(*dir)
		if err != nil {
			return err
		}
		if _, err := os.Stdout.Write(body); err != nil {
			return fmt.Errorf("agent install-agents-md --tar: write stdout: %w", err)
		}
		return nil
	}
	written, err := agentSkills.WriteAgentsSection(*dir)
	if err != nil {
		return err
	}
	for _, p := range written {
		slog.InfoContext(ctx, "agent install-agents-md: wrote", slog.String("path", p))
	}
	printAgentInstallNextSteps(written)
	return nil
}

// printAgentInstallNextSteps prints an actionable hint after install, gated on
// the user-controlled hints preference so MAGUS_HINTS_ENABLED=false silences it.
func printAgentInstallNextSteps(written []string) {
	if !interactive.HintsEnabled() || len(written) == 0 {
		return
	}
	interactive.Emit(os.Stderr, fmt.Sprintf("installed %d file(s); commit them so your team and agents share them", len(written)))
	interactive.Emit(os.Stderr, "the skills point at MAGUS.md's routing table:  magus describe graph -o markdown")
	interactive.Emit(os.Stderr, "safety: consider a line in your CLAUDE.md/AGENTS.md so parallel agents cannot wipe each other's work:")
	interactive.Emit(os.Stderr, "  \""+vcsSafetyRule+"\"")
	interactive.Emit(os.Stderr, "starter AGENTS.md you can own and tweak (prints, never writes):  magus agent sample")
}

// vcsSafetyRule is the one always-on version-control rule worth carrying in a
// CLAUDE.md/AGENTS.md: it stops one agent's whole-tree revert from destroying
// another's uncommitted work. Shared by the install hint and the sample doc.
const vcsSafetyRule = "Version control is the orchestrator's job: do it yourself, never delegate it to a subagent, and never discard or revert uncommitted changes across the whole tree to verify a build - build in place. A whole-tree revert permanently destroys a concurrent agent's uncommitted work."

// agentSampleDoc returns a complete, opinionated-but-tweakable AGENTS.md starter a
// developer can paste and adapt. It is print-only (magus agent sample): unlike
// `agent install codex`, which manages a marked magus section inside an existing
// AGENTS.md, this hands over a whole file to own, so magus never risks clobbering
// one. The magus block reproduces agents-section.md verbatim.
func agentSampleDoc() string {
	return "# AGENTS.md\n\n" +
		"<!-- A starter for AI agents working in this repo. Own and edit this file:\n" +
		"     fill in the project-specific sections below. The magus block reproduces\n" +
		"     the guidance `magus agent install` would otherwise manage for you. -->\n\n" +
		"## Project\n\n" +
		"<!-- What this repo is, its primary language(s), and where the entry points\n" +
		"     and top-level layout live. A few sentences. -->\n\n" +
		"## Conventions\n\n" +
		"<!-- The non-obvious house rules an agent cannot infer from the code:\n" +
		"     naming, error handling, comment style, and what NOT to touch. -->\n\n" +
		"## Version control\n\n" +
		"- " + vcsSafetyRule + "\n\n" +
		strings.TrimSpace(agentSkills.Section()) + "\n"
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
	Decision      string `json:"decision"`          // deny | advise | pass
	Reason        string `json:"reason,omitempty"`  // deny: the block reason, written for the model
	Context       string `json:"context,omitempty"` // advise: context to inject alongside the allowed call
}

const guardSchemaVersion = 1

// agentHookCmd implements `magus agent hook`: evaluate one shell command
// against the guard rules and emit a verdict. The command arrives as arguments,
// as raw stdin, or extracted from a JSON event on stdin via --from-json
// <dot.path> - whichever the calling agent host can produce. The verdict goes
// out through the standard -o arm, so a host-specific response shape is a
// documented template, not code. A guard must fail open: an unreadable event is
// a pass, never an error that would block every tool call.
func agentHookCmd(ctx context.Context, in io.Reader, out io.Writer, args []string) error {
	fset := flag.NewFlagSet("agent hook", flag.ContinueOnError)
	fromJSON := fset.String("from-json", "", "Extract the command from a JSON document on stdin at this dot-separated path (e.g. tool_input.command)")
	asPath := fset.Bool("path", false, "Judge the input as a FILE PATH an edit is about to write, not as a shell command: a declared target output is denied")
	output := fset.String("output", "", outputFormatHelp)
	fset.StringVar(output, "o", "", "Short for --output")
	fset.Usage = func() { agentUsage(os.Stderr) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	opts, err := ResolveOutput(*output)
	if err != nil {
		return err
	}

	verdict := guardVerdict{SchemaVersion: guardSchemaVersion, Decision: "pass"}
	if *asPath {
		if path, ok := readGuardCommand(in, fset.Args(), *fromJSON); ok {
			if reason := denyGeneratedWrite(ctx, path); reason != "" {
				verdict.Decision = "deny"
				verdict.Reason = reason
			}
		}
		return writeGuardVerdict(out, opts, verdict)
	}
	if command, ok := readGuardCommand(in, fset.Args(), *fromJSON); ok {
		switch v := evaluateBashGuard(command); {
		case v.Deny != "":
			verdict.Decision = "deny"
			verdict.Reason = v.Deny
		case v.Context != "":
			verdict.Decision = "advise"
			verdict.Context = v.Context
		}
	}
	return writeGuardVerdict(out, opts, verdict)
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

// denyGeneratedWrite reports why an edit to path must be blocked, or "" to allow
// it. Unlike the command rules this is not a heuristic: magus knows every
// target's declared outputs, so a role=output path is generated by definition
// and an edit to it is guaranteed to be overwritten by the next run.
//
// Fails OPEN on every uncertainty - no workspace, an unreadable one, an
// unclaimed path - because a guard that blocks edits when it cannot tell is
// worse than one that misses some.
func denyGeneratedWrite(ctx context.Context, path string) string {
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
	files := ws.DescribeFiles([]string{path})
	if len(files.Files) != 1 || files.Files[0].Role != "output" {
		return ""
	}
	f := files.Files[0]
	owner := f.Project
	if owner == "" {
		owner = "."
	}
	return fmt.Sprintf("%s is a DECLARED OUTPUT of project %s - it is generated, and the next run of its producing target overwrites whatever you write here. This is not a style rule: magus reads the target's declared output globs, so the classification is definitive. Edit the SOURCE that produces it, then run `magus run generate %s` (or the producing target) and commit the regenerated file with your source change. `magus describe file %s` shows the classification.", f.Path, owner, owner, f.Path)
}

// readGuardCommand resolves the command string from the three input forms:
// --from-json extraction, positional arguments (joined), or raw stdin (also the
// "-" positional). The boolean is false when no command could be read - the
// caller's fail-open path.
func readGuardCommand(in io.Reader, args []string, fromJSON string) (string, bool) {
	if fromJSON != "" {
		doc, err := io.ReadAll(in)
		if err != nil {
			return "", false
		}
		s, err := extractJSONString(doc, fromJSON)
		if err != nil {
			// Visible in the host's debug log, invisible to the session: fail open.
			fmt.Fprintln(os.Stderr, "agent hook: "+err.Error()+" (failing open)")
			return "", false
		}
		return s, true
	}
	if len(args) > 0 && (len(args) != 1 || args[0] != "-") {
		return strings.Join(args, " "), true
	}
	b, err := io.ReadAll(in)
	if err != nil || len(bytes.TrimSpace(b)) == 0 {
		return "", false
	}
	return string(b), true
}

// extractJSONString unmarshals doc and walks a dot-separated path of object
// keys to a string value.
func extractJSONString(doc []byte, path string) (string, error) {
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return "", fmt.Errorf("--from-json: stdin is not valid JSON: %w", err)
	}
	for _, key := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return "", fmt.Errorf("--from-json: %s: no object to descend into at %q", path, key)
		}
		if v, ok = m[key]; !ok {
			return "", fmt.Errorf("--from-json: %s: key %q not found", path, key)
		}
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("--from-json: %s: value is not a string", path)
	}
	return s, nil
}

// bashGuardVerdict classifies one Bash command line. Deny blocks the call with a
// reason the model sees; Context lets it proceed and injects a reminder.
type bashGuardVerdict struct {
	Deny    string
	Context string
}

// The guard patterns. [^&|;]* keeps a flag search inside one segment of a
// compound command, so `git reset && tool --hard-mode` does not false-positive.
var (
	guardStashRe     = regexp.MustCompile(`\bgit\s+stash\b`)
	guardStashSafeRe = regexp.MustCompile(`\bgit\s+stash\s+(list|show|pop|apply|drop|branch)\b`)
	guardResetRe     = regexp.MustCompile(`\bgit\s+reset\b[^&|;]*--hard`)
	guardCheckoutRe  = regexp.MustCompile(`\bgit\s+checkout\s+(--\s+)?\.(\s|$)`)
	guardRestoreRe   = regexp.MustCompile(`\bgit\s+restore\b[^&|;]*\s\.(\s|$)`)
	guardCleanRe     = regexp.MustCompile(`\bgit\s+clean\b[^&|;]*\s-\w*[fdxX]`)
	guardStageRe     = regexp.MustCompile(`\bgit\s+(commit|add)\b`)
	// A SCOPED revert: `git checkout -- <paths>` / `git restore <paths>`. The
	// whole-tree forms above already deny; this one is legitimate often enough
	// that it only advises, but it is the shape of the most common wrong reflex
	// an agent has about generated files.
	// `git checkout ... -- <paths>` needs the `--` separator to be a revert at
	// all; without it the argument is a branch (`git checkout main`, `-b foo`),
	// which is not this rule's business. `git restore` targets worktree files by
	// definition, so its bare form counts.
	guardScopedRevertRe = regexp.MustCompile(`\bgit\s+checkout\b[^&|;]*\s--\s|\bgit\s+restore\b`)
	// Raw-tool invocations that bypass nothing, so advising on them is pure noise.
	// RE2 has no lookahead, so these are matched BEFORE the raw-tool rule rather
	// than excluded inside it.
	//
	//   go build -o <path>  builds a binary to an explicit path - the documented
	//                       dev-loop build, not a target's build artifact.
	//   gofmt -l / -d       lists or diffs; only -w rewrites the tree.
	guardRawToolExemptRe = regexp.MustCompile(`\bgo\s+build\b[^&|;]*\s-o\s|\bgofmt\s+-[ld]\b`)
	guardRawToolRe       = regexp.MustCompile(`\bgo\s+(test|build|vet|generate)\b|\bgofmt\b|\bgoimports\b|\bgolangci-lint\b|\bmockery\b|\bbuf\s+(generate|lint|breaking)\b|\bnpm\s+(test|run|exec)\b|\bnpx\s|\bpnpm\b|\byarn\b|\beslint\b|\bprettier\b|\bbiome\b|\bvitest\b|\bjest\b|\bpytest\b|\bruff\b|\bblack\b|\bmypy\b|\btsc\b|\bcargo\s+(test|build|check|clippy|fmt)\b|\brustfmt\b`)
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
	// magus must be the COMMAND, not merely a substring: it is anchored to the
	// start of a command segment (start of line, or after ; && || |) and allows a
	// leading path or env assignments. Matching a bare \bmagus\b fired on
	// `grep x cmd/magus/*_test.go | head`, where the word is only a path.
	magusCmd          = `(^|[;&|]|&&|\|\|)\s*(\w+=\S+\s+)*(\S*/)?magus\s`
	guardMagusPipeRe  = regexp.MustCompile(magusCmd + `[^|;&]*\|\s*(tail|head|grep|wc|sed|awk|cut|sort|uniq)\b`)
	guardMagusRedirRe = regexp.MustCompile(magusCmd + `[^|;&]*2>&1`)
)

const (
	vcsGuardContext = "magus workspace: classify the dirty tree before staging or committing: magus describe file $(git diff --name-only). role=output paths are generated - never hand-edit them; regenerate and commit them with their source change. Load the magus-vcs skill for the commit checklist if not already loaded."
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
	runGuardContext = "magus workspace: run this through magus, not the raw tool - a raw tool bypasses the cache, the sandbox, and affected tracking. Escalate only as far as you actually need:\n" +
		"  1. TOP-LEVEL TARGET (use this almost always):  magus run test|build|lint|format|generate [<project>]  - MAGUS.md lists every target\n" +
		"  2. ONE SPELL OP, still through magus, when a whole target is too broad:  magus run <spell>::<op> [<project>]  (e.g. magus run go::go-test libs/foo). `magus describe spell <name>` lists a spell's ops.\n" +
		"To see the exact command a target or op would run, WITHOUT running it, add --dry-run: `magus run go::go-test libs/foo --dry-run` prints `$ go test ./...`. Use that to learn what magus does under the hood instead of guessing and reaching for the raw tool.\n" +
		"Args after `--` are forwarded, so a specific flag is NOT a reason to reach for the raw tool: `magus run go::go-test libs/foo -- -run TestX` runs `go test ./... -run TestX`, and a magusfile target receives them as its `args: [str]` parameter. Narrow by PROJECT too - `magus run test libs/foo` runs less. Load the magus-run skill if not already loaded."
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
		"`magus query <symbol>` returns 0 for code symbols - that is refs's job, not query's; do not conclude the graph is empty. If refs reports no symbol index, build it once with `magus graph build` (the daemon keeps it current while `magus server start` runs).\n" +
		"If you are searching for raw TEXT rather than a symbol or an entity (a string literal, a comment, a config value), grep is the right tool and magus has no replacement - carry on. Load the magus-query skill for the full grammar."

	// Named for what the agent should do instead, not for what it did wrong: the
	// flags are the actionable part, and a weaker model needs the exact spelling.
	outputGuardContext = "magus workspace: do not pipe or redirect magus output to trim it - magus already has output control, and a pipe discards the parts you then have to guess at. Use -s/--silent (progress suppressed; a failure prints only its likely diagnostics plus the full-log path), -o json / -o name / -o template=<go-template> for machine-readable output, and `magus query output <ref>` for a failing target's complete captured log. Exit status is the pass/fail signal; 2>&1 is never needed because magus already writes diagnostics where you are reading."
)

func denyWholeTree(op string) string {
	return "whole-tree " + op + " destroys uncommitted and untracked work, including a concurrent agent's. Verify builds in place (magus run build / magus affected ci); building never requires a clean tree. If you truly need a pristine tree, use a throwaway git worktree. See the magus-vcs skill."
}

// evaluateBashGuard applies the guard rules in severity order: destructive
// whole-tree git operations deny; staging/committing and raw language tools
// proceed with an injected reminder.
func evaluateBashGuard(command string) bashGuardVerdict {
	switch {
	case guardStashRe.MatchString(command) && !guardStashSafeRe.MatchString(command):
		return bashGuardVerdict{Deny: denyWholeTree("git stash")}
	case guardResetRe.MatchString(command):
		return bashGuardVerdict{Deny: denyWholeTree("git reset --hard")}
	case guardCheckoutRe.MatchString(command):
		return bashGuardVerdict{Deny: denyWholeTree("git checkout .")}
	case guardRestoreRe.MatchString(command):
		return bashGuardVerdict{Deny: denyWholeTree("git restore .")}
	case guardCleanRe.MatchString(command):
		return bashGuardVerdict{Deny: denyWholeTree("git clean")}
	case guardStageRe.MatchString(command):
		return bashGuardVerdict{Context: vcsGuardContext}
	case guardScopedRevertRe.MatchString(command):
		return bashGuardVerdict{Context: revertGuardContext}
	case guardRawToolExemptRe.MatchString(command):
		return bashGuardVerdict{}
	case guardRawToolRe.MatchString(command):
		return bashGuardVerdict{Context: runGuardContext}
	case guardMagusPipeRe.MatchString(command), guardMagusRedirRe.MatchString(command):
		return bashGuardVerdict{Context: outputGuardContext}
	case guardCdMagusRe.MatchString(command):
		return bashGuardVerdict{Context: cwdGuardContext}
	case guardCodeSearchRe.MatchString(command):
		return bashGuardVerdict{Context: searchGuardReason}
	}
	return bashGuardVerdict{}
}
