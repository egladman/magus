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
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/agent"
	json "github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/internal/interactive"
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
	case "notify":
		return agentNotifyCmd(ctx, os.Stdin, os.Stdout, args[1:])
	case "-h", "--help", "help":
		agentUsage(os.Stderr)
		return nil
	default:
		return usagef("magus agent: unknown subcommand %q (want install, install-agents-md, sample, hook, or notify)", args[0])
	}
}

func agentUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus agent <install|install-agents-md|sample|hook|notify> [flags]")
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
	fmt.Fprintln(w, "  notify             turn one agent-host event (waiting for input, needs")
	fmt.Fprintln(w, "                     approval, finished) into an attention record, and")
	fmt.Fprintln(w, "                     optionally a desktop notification")
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
	fmt.Fprintln(w, "                 the imperative steps without the rationale, for a reader")
	fmt.Fprintln(w, "                 that infers the why. Both permutations are hand-authored")
	fmt.Fprintln(w, "                 from one source and share one content digest, so they go")
	fmt.Fprintln(w, "                 stale together and `graph verify` treats them alike.")
	fmt.Fprintln(w, "  --tar          stream a tar archive to stdout instead of writing files")
	fmt.Fprintln(w, "  --global       allow absolute destination paths in write mode")
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
	// See graph_verify.go: the display flags are global, so every command takes
	// them. `agent install ... -s` previously died on an undefined flag, and with
	// stderr redirected that looked exactly like a successful install - the
	// skills were never written and nothing said so.
	bindDisplayFlags(fset)
	dir := fset.String("dir", ".", "Repo directory to install into")
	force := fset.Bool("force", false, "Overwrite existing installed skill files (write mode)")
	simple := fset.Bool("simple", false, "Install the shorter curated permutation of each skill: the imperative steps without the rationale, for a reader that infers the why")
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

// agentInstallAgentsMDCmd renders the managed magus section for <dir>/AGENTS.md
// and either writes it back in place or streams it on stdout (--tar).
// Bytes outside the begin/end markers are preserved from any existing file.
func agentInstallAgentsMDCmd(ctx context.Context, args []string) error {
	fset := flag.NewFlagSet("agent install-agents-md", flag.ContinueOnError)
	bindDisplayFlags(fset)
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
	printAgentInstallNextSteps(*dir, written, agent.VariantFull)
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
	// MAGUS.md is regenerated for HUMAN readers; the skills send agents to the live
	// verbs instead, because a generated index is only true as of its last run.
	interactive.Emit(os.Stderr, "regenerate MAGUS.md for human readers:  magus describe graph -o markdown  (the skills send agents to the live verbs - magus describe targets, magus ls)")
	interactive.Emit(os.Stderr, "safety: consider a line in your repo's agent instruction file so parallel agents cannot wipe each other's work:")
	interactive.Emit(os.Stderr, "  \""+vcsSafetyRule+"\"")
	interactive.Emit(os.Stderr, "starter AGENTS.md you can own and tweak (prints, never writes):  magus agent sample")
}

// vcsSafetyRule is the one always-on version-control rule worth carrying in a
// repo's agent instruction file: it stops one agent's whole-tree revert from destroying
// another's uncommitted work. Shared by the install hint and the sample doc.
const vcsSafetyRule = "Version control is the orchestrator's job: do it yourself, never delegate it to a subagent, and never discard or revert uncommitted changes across the whole tree to verify a build - build in place. A whole-tree revert permanently destroys a concurrent agent's uncommitted work."

// agentSampleDoc returns a complete, opinionated-but-tweakable AGENTS.md starter a
// developer can paste and adapt. It is print-only (magus agent sample): unlike
// a host-named subcommand, which would manage a marked magus section inside an existing
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
	asPath := fset.Bool("path", false, "Judge the input as a FILE PATH an edit is about to write, not as a shell command: editing a declared target output is advised against")
	// The whole display set, not a hand-rolled -o: this command used to define
	// its own output flag and so silently lacked -s, -q, -v and --tee. That gap
	// is the reason for the rule - a flag accepted on most commands teaches
	// callers it is unreliable everywhere.
	bindDisplayFlags(fset)
	fset.Usage = func() { agentUsage(os.Stderr) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	opts, err := ResolveOutput(global.output)
	if err != nil {
		return err
	}

	verdict := guardVerdict{SchemaVersion: guardSchemaVersion, Decision: "pass"}
	if *asPath {
		if path, ok := readGuardCommand(in, fset.Args(), *fromJSON); ok {
			// The generated-output rule is definitive (it reads declared globs), so it
			// speaks first; the memory nudge is a heuristic on the filename and only
			// fills the silence it leaves.
			context := adviseGeneratedWrite(ctx, path)
			if context == "" {
				context = adviseMemoryWrite(path)
			}
			if context != "" {
				verdict.Decision = "advise"
				verdict.Context = context
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

// A pass-through wrapper runs ANOTHER command, with the toolchain, environment,
// or timeout adjusted first. It is never itself the finding: `mise exec` is how
// this workspace reaches its pinned Go and `env -u GOROOT` is load-bearing in
// every documented build here, so a guard that denied them would be denying the
// house style. The guard peels them off and judges the payload, which is why
// `mise exec -- magus run test` passes and the same wrapper around `go test`
// does not.
//
// `mise run` is deliberately absent: it runs a DECLARED mise task, not a
// smuggled command, and peeling it would misattribute the task's contents.
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
			// mise exec [tool@version ...] -- cmd, and the `mise x` alias.
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

// guardRawTools maps a program to the subcommands that have a magus equivalent.
// An empty set means the program is covered whatever it is asked to do.
//
// Structured, not a pattern: the key is a program NAME the parser resolved, so a
// tool named in prose, in a commit message, or inside a quoted argument can
// never reach this map. That is what the cmdPos anchor was approximating.
var guardRawTools = map[string]map[string]bool{
	"go":            {"test": true, "build": true, "vet": true, "generate": true, "mod": true},
	"buf":           {"generate": true, "lint": true, "breaking": true},
	"npm":           {"test": true, "run": true, "exec": true},
	"cargo":         {"test": true, "build": true, "check": true, "clippy": true, "fmt": true},
	"gofmt":         {},
	"goimports":     {},
	"golangci-lint": {},
	"govulncheck":   {},
	"mockery":       {},
	"npx":           {},
	"pnpm":          {},
	"yarn":          {},
	"eslint":        {},
	"prettier":      {},
	"biome":         {},
	"vitest":        {},
	"jest":          {},
	"pytest":        {},
	"ruff":          {},
	"black":         {},
	"mypy":          {},
	"tsc":           {},
	"rustfmt":       {},
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
		if rawToolDenied(c) {
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

// rawToolDenied reports whether one resolved command has a magus equivalent that
// should be used instead.
//
// The exemptions are the commands that bypass nothing, where a deny would be
// pure obstruction:
//
//   - `go mod` other than tidy/vendor, which read rather than write.
//   - `gofmt -l` / `-d`   lists or diffs; only -w rewrites the tree.
//   - any `--version`     asks what is installed. It reads no sources, writes no
//     outputs, and has no magus equivalent.
//
// `go build` is NOT exempt at any output path, and the `-o /tmp/...` dev-loop
// build that used to be exempt is the specific case this closed. It produces a
// binary, which makes it a write, and the write rule has no exceptions - the
// toolchain verb is what has to change, not the destination. `magus run build`
// covers it and `magus run go::go-build` is the one-op form.
//
// That is the whole justification. An earlier version of this comment also
// blamed raw builds for poisoning the Go build cache into a link-time
// fingerprint mismatch; that claim was tested and is FALSE. The mismatch
// reproduces with no raw build in the session and survives `go clean -cache`,
// so it has some other cause. Do not restore the claim without evidence.
func rawToolDenied(c guardCommand) bool {
	subs, known := guardRawTools[c.Name]
	if !known {
		return false
	}
	for _, a := range c.Args {
		if a == "--version" || a == "-version" || a == "-V" {
			return false
		}
	}
	if len(subs) > 0 {
		if len(c.Args) == 0 || !subs[c.Args[0]] {
			return false
		}
	}

	switch c.Name {
	case "go":
		if c.Args[0] == "mod" {
			return len(c.Args) > 1 && (c.Args[1] == "tidy" || c.Args[1] == "vendor")
		}
	case "gofmt":
		for _, a := range c.Args {
			if a == "-l" || a == "-d" {
				return false
			}
		}
	}
	return true
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
			// Reading and restoring a stash is safe; creating one moves the tree.
			if len(rest) > 0 && slices.Contains([]string{"list", "show", "pop", "apply", "drop", "branch"}, rest[0]) {
				continue
			}
			return bashGuardVerdict{Deny: denyWholeTree("git stash")}, true
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
// These remain ONLY as the unparseable-line fallback: gitGuard above is the
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
	// magus must be the COMMAND, not merely a substring: it is anchored to the
	// start of a command segment (start of line, or after ; && || |) and allows a
	// leading path or env assignments. Matching a bare \bmagus\b fired on
	// `grep x cmd/magus/*_test.go | head`, where the word is only a path.
	magusCmd          = `(^|[;&|]|&&|\|\|)\s*(\w+=\S+\s+)*(\S*/)?magus\s`
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
		"Args after `--` are forwarded, so a specific flag is NOT a reason to reach for the raw tool: `magus run go::go-test libs/foo -- -run TestX` runs `go test ./... -run TestX`, and a magusfile target receives them as its `args: [str]` parameter. Narrow by PROJECT too - `magus run test libs/foo` runs less. Load the magus-run skill if not already loaded.\n" +
		"Do NOT retry this behind a wrapper. The guard reads the command being RUN, not the one being typed, so `mise exec -- ...`, `env -u GOROOT ...`, a `VAR=value` prefix and `bash -c '...'` all reach the same verdict. Those wrappers are fine in front of a magus command - `mise exec -- magus run test` is exactly right, and is what to reach for when the point of the wrapper was the pinned toolchain."
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

	pushGuardContext = "magus workspace: `magus affected ci` is the gate before publishing - it runs the full pipeline over every project the diff reaches, including ones you never edited. Run it if you have not since your last change. If you are pushing deliberate work-in-progress, or you already ran it, push. Load the magus-run skill if not already loaded."

	// Named for what the agent should do instead, not for what it did wrong: the
	// flags are the actionable part, and a weaker model needs the exact spelling.
	// denyStageAll routes to deliberate staging. `git add -A` is the single command
	// most likely to turn a focused change into an unreviewable one: it sweeps every
	// regenerated output and every unrelated formatting fix a target just wrote into
	// a commit about something else. Measured: one such call put 69 files - a whole
	// regenerated docs site plus five untouched source files - into a commit about
	// four collection methods.
	denyStageAll = "staging everything has an exact equivalent in magus workspaces, so this is denied rather than explained. Use it:\n" +
		"  magus vcs add --dry-run      # classify the dirty tree, stage nothing\n" +
		"  magus vcs add                # stage the declared sources AND the generated outputs they produced\n" +
		"It groups what it stages by role and REPORTS every undeclared path instead of sweeping it in, which is the one thing `git add -A` cannot do. `magus vcs add <path>...` narrows it, and `--untracked` opts the undeclared ones back in when one of them is genuinely a new source file.\n" +
		"Why this is not just style: a magus target writes its declared outputs as it runs, so a tree is routinely dirty with generated files you did not edit. `git add -A` commits them with no signal that it happened, and it also picks up build residue. Staging specific paths by hand (`git add <path> ...`) is still fine; confirm either way with `git diff --cached --stat` BEFORE committing. Load the magus-vcs skill if not already loaded."

	outputGuardContext = "magus workspace: do not pipe or redirect magus output to trim it - magus already has output control, and a pipe discards the parts you then have to guess at. Use -s/--silent (progress suppressed; a failure prints only its likely diagnostics plus the full-log path), -o json / -o name / -o template=<go-template> for machine-readable output, and `magus query output <ref>` for a failing target's complete captured log. Exit status is the pass/fail signal; 2>&1 is never needed because magus already writes diagnostics where you are reading. The one command you MAY pipe into a filter is `magus query output <ref>`: that returns a target's raw captured tool log, which has no schema for magus to project, so searching it is a real need. Every other verb emits a structured record that -o already shapes exactly."
)

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
	cmds, parsed := parseGuardCommands(command)
	if parsed {
		if v, matched := gitGuard(cmds); matched {
			return v
		}
	} else if v, matched := gitGuardFallback(command); matched {
		return v
	}

	rawToolCmd, rawToolDeny := firstRawToolDenied(command)
	switch {
	case rawToolDeny:
		return bashGuardVerdict{Deny: explainDeny(command, rawToolCmd, runGuardContext)}
	case magusPipedToFilter(command):
		return bashGuardVerdict{Deny: outputGuardContext}
	case guardMagusRedirRe.MatchString(command):
		return bashGuardVerdict{Context: outputGuardContext}
	case guardCdMagusRe.MatchString(command):
		return bashGuardVerdict{Context: cwdGuardContext}
	case guardCodeSearchRe.MatchString(command):
		return bashGuardVerdict{Context: searchGuardReason}
	}
	return bashGuardVerdict{}
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
	var installed int64
	for _, rel := range written {
		if info, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			installed += info.Size()
		}
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
