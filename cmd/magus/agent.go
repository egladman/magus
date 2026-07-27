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
// `install` writes the embedded skills into explicitly named destinations, and
// `hook` evaluates one shell command to a guard verdict. Destinations and event
// shapes are explicit arguments, never auto-detected (per the explicit-and-
// granular preference); writing into a repo's agent-config dirs happens only
// through this command, never as a side effect of another.
func agentCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return agentUsageErr()
	}
	switch args[0] {
	case "install":
		return agentInstallCmd(ctx, args[1:])
	case "sample":
		return agentSampleCmd()
	case "hook":
		return agentHookCmd(os.Stdin, os.Stdout, args[1:])
	case "-h", "--help", "help":
		agentUsage(os.Stderr)
		return nil
	default:
		return fmt.Errorf("agent: unknown subcommand %q (try: install, sample, hook)", args[0])
	}
}

func agentUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus agent install <dir>... [--agents-md] [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Install the magus agent skills into the current repo so an agent knows")
	fmt.Fprintln(w, "how to use the knowledge graph instead of grepping, run work through")
	fmt.Fprintln(w, "targets instead of raw tools, triage generated files, and ground")
	fmt.Fprintln(w, "refactoring proposals in the graph.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "magus is agent-host agnostic: the skills are one shared source in the")
	fmt.Fprintln(w, "cross-agent Agent Skills format, and you name the directory your host")
	fmt.Fprintln(w, "discovers skills in. Common destinations:")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "  .agents/skills     Agent Skills spec generic project location")
	fmt.Fprintln(w, "  .claude/skills     Claude Code")
	fmt.Fprintln(w, "  .opencode/skills   OpenCode")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "  --agents-md        maintain a managed magus section in AGENTS.md for")
	fmt.Fprintln(w, "                     hosts that read that contract instead of skill dirs")
	fmt.Fprintln(w, "                     (created if absent, replaced in place on re-install,")
	fmt.Fprintln(w, "                     bytes outside the markers never touched)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Other subcommands:")
	fmt.Fprintln(w, "  sample     print a starter AGENTS.md to stdout to own and tweak; never")
	fmt.Fprintln(w, "             writes a file, so it cannot clobber an existing one")
	fmt.Fprintln(w, "  hook       evaluate one shell command against the magus guard rules and")
	fmt.Fprintln(w, "             emit a deny/advise/pass verdict. Input: arguments, raw stdin,")
	fmt.Fprintln(w, "             or --from-json <dot.path> to extract it from a JSON event on")
	fmt.Fprintln(w, "             stdin. Shape the verdict for your host with -o json|yaml|")
	fmt.Fprintln(w, "             template=<go-template> (bare -o template lists the fields)")
}

func agentUsageErr() error {
	agentUsage(os.Stderr)
	return fmt.Errorf("agent: a subcommand is required (try: install)")
}

// agentInstallCmd writes the embedded skills into every destination directory
// named as a positional (repo-relative under --dir), and maintains the AGENTS.md
// section when --agents-md is set. Destinations are explicit, never inferred
// from an agent-host name: magus writes the standard format where told and stays
// out of the host-specific business.
func agentInstallCmd(ctx context.Context, args []string) error {
	fset := flag.NewFlagSet("agent install", flag.ContinueOnError)
	dir := fset.String("dir", ".", "Repo directory to install into")
	force := fset.Bool("force", false, "Overwrite existing installed skill files")
	agentsMD := fset.Bool("agents-md", false, "Maintain the managed magus section in AGENTS.md")
	fset.Usage = func() { agentUsage(os.Stderr) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	dests := fset.Args()
	if len(dests) == 0 && !*agentsMD {
		agentUsage(os.Stderr)
		return fmt.Errorf("agent install: name at least one destination directory (e.g. .claude/skills) or pass --agents-md")
	}

	var written []string
	for _, dest := range dests {
		w, err := agentSkills.InstallSkillTree(*dir, dest, *force)
		if err != nil {
			return err
		}
		written = append(written, w...)
	}
	if *agentsMD {
		w, err := agentSkills.InstallAgentsSection(*dir)
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
func agentHookCmd(in io.Reader, out io.Writer, args []string) error {
	fset := flag.NewFlagSet("agent hook", flag.ContinueOnError)
	fromJSON := fset.String("from-json", "", "Extract the command from a JSON document on stdin at this dot-separated path (e.g. tool_input.command)")
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
	guardRawToolRe   = regexp.MustCompile(`\bgo\s+(test|build|vet)\b|\bnpm\s+(test|run|exec)\b|\bnpx\s|\bpnpm\b|\byarn\b|\beslint\b|\bprettier\b|\bpytest\b|\btsc\b|\bcargo\s+(test|build|check|clippy)\b`)
)

const (
	vcsGuardContext = "magus workspace: classify the dirty tree before staging or committing: magus describe file $(git diff --name-only). role=output paths are generated - never hand-edit them; regenerate and commit them with their source change. Load the magus-vcs skill for the commit checklist if not already loaded."
	runGuardContext = "magus workspace: a magus target likely covers this (magus run build / test / lint / format / generate; MAGUS.md lists every target). Raw language tools bypass the cache, the sandbox, and affected tracking. Load the magus-run skill if not already loaded; if no target covers this work, proceed."
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
	case guardRawToolRe.MatchString(command):
		return bashGuardVerdict{Context: runGuardContext}
	}
	return bashGuardVerdict{}
}
