package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/types"
)

// skillFS holds the agent-skill sources embedded at build time, so the knowledge
// of HOW to use the knowledge graph travels with the binary and installs into any
// consuming repo. The sources are static (they teach the tool surface, which ships
// with the magus version) and never embed repo specifics - those live in the
// generated MAGUS.md the skill defers to.
//
// One source tree serves every platform: the Agent Skills format (SKILL.md with
// name+description frontmatter) is a cross-agent spec, so skill-dir platforms get
// identical bytes at different destinations, and platforms without skill support
// get the distilled AGENTS.md section instead. No per-model skill bodies exist.
//
//go:embed skills
var skillFS embed.FS

// agentsSection is the distilled always-on block installed into AGENTS.md for
// platforms that read that contract instead of skill directories (Codex, Aider,
// and most other AGENTS.md-reading agents). Same rules as the skills, compressed.
//
//go:embed agents-section.md
var agentsSection string

// agentSkillVersion is bumped whenever the installed skill content, or the tool
// surface it documents, changes. Together with the knowledge schema version it
// stamps the install footer, so the drift check (magus graph verify) can tell a
// stale installed skill from a current one without diffing bytes.
//
//	v1: initial skill (verbs, grammar, reading results, MCP, --global, pagination)
//	v2: teach CODEOWNERS ownership and the owns relation
//	v3: teach the refs subcommand / magus_refs (SCIP symbol def+references)
//	v4: teach the * wildcard in the query grammar
//	v5: teach the graph diff subcommand (PR blast-radius against a baseline export)
//	v6: teach the opt-in @vcs git-history attrs on file nodes
//	v7: purpose-named skill set (magus-query, magus-run, magus-vcs,
//	    magus-architecture, magus-memory); multi-platform install (claude/
//	    opencode/agents skill dirs, AGENTS.md section for codex); query-first
//	    fast path; describe-file triage; durable magus_memory workflow
//	v8: output-control doctrine (-s/-q silence for runs, -o json as the
//	    machine-reading default, the JSON envelope shape, MCP-first with quiet
//	    CLI fallback); magus-vcs frontmatter name aligned with its directory
//	v9: add magus-docs (navigate magus's own documentation via llms.txt /
//	    search-index.json, the URL + section scheme, and the in-page nav axes)
//	v10: magus-vcs teaches environmental-vs-real drift; install injects
//	    open-standard provenance frontmatter (license, compatibility, metadata)
//	v11: magus-run teaches CWD-relative project scope; tighten the prose
//	v12: trigger-first frontmatter descriptions (literal command cues) for
//	     magus-vcs/magus-run/magus-query; agents-section moment-to-skill routing
//	     table; claude install merges a PreToolUse guard hook into
//	     .claude/settings.json (magus agent hook)
const agentSkillVersion = 12

// skillDirPlatforms maps each skill-directory platform to its repo-relative
// destination. The bytes written are identical across platforms - the Agent
// Skills spec is shared - only the discovery location differs. "agents" is the
// spec's generic project location, read by npx skills and compliant frameworks.
var skillDirPlatforms = map[string]string{
	"claude":   ".claude/skills",
	"opencode": ".opencode/skills",
	"agents":   ".agents/skills",
}

// agentsMDPlatform is the platform whose install target is a managed section in
// AGENTS.md rather than a skill directory. Named for Codex (the largest reader),
// but the section serves every agent that honors the AGENTS.md contract.
const agentsMDPlatform = "codex"

// platformNames returns every supported platform, skill-dir ones sorted first,
// for stable usage/error text.
func platformNames() []string {
	names := make([]string, 0, len(skillDirPlatforms)+1)
	for name := range skillDirPlatforms {
		names = append(names, name)
	}
	sort.Strings(names)
	return append(names, agentsMDPlatform)
}

// agentCmd implements `magus agent <subcommand>`: the agent-integration surface.
// Today the one subcommand is `install <platform>`, which writes the embedded skill into
// the consuming repo. Platform is an explicit argument, never auto-detected (per
// the explicit-and-granular preference); writing into a repo's agent-config dirs
// happens only through this command, never as a side effect of another.
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
		return agentHookCmd(os.Stdin, os.Stdout)
	case "-h", "--help", "help":
		agentUsage(os.Stderr)
		return nil
	default:
		return fmt.Errorf("agent: unknown subcommand %q (try: install, sample, hook)", args[0])
	}
}

func agentUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus agent install <platform> [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Install the magus agent skills into the current repo so an agent knows")
	fmt.Fprintln(w, "how to use the knowledge graph instead of grepping, run work through")
	fmt.Fprintln(w, "targets instead of raw tools, triage generated files, and ground")
	fmt.Fprintln(w, "refactoring proposals in the graph.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "The skills are one shared source (magus-query, magus-run, magus-vcs,")
	fmt.Fprintln(w, "magus-architecture, magus-memory, magus-docs); platforms differ only in")
	fmt.Fprintln(w, "where they discover them.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Platforms:")
	fmt.Fprintln(w, "  claude     write .claude/skills/ and merge a PreToolUse guard hook into")
	fmt.Fprintln(w, "             .claude/settings.json (Claude Code); --hooks=false skips the hook")
	fmt.Fprintln(w, "  opencode   write .opencode/skills/ (OpenCode)")
	fmt.Fprintln(w, "  agents     write .agents/skills/ (Agent Skills spec generic location)")
	fmt.Fprintln(w, "  codex      write a managed magus section into AGENTS.md (Codex and")
	fmt.Fprintln(w, "             any other AGENTS.md-reading agent)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Other subcommands:")
	fmt.Fprintln(w, "  sample     print a starter AGENTS.md to stdout to own and tweak; never")
	fmt.Fprintln(w, "             writes a file, so it cannot clobber an existing one")
	fmt.Fprintln(w, "  hook       plumbing invoked by the installed Claude Code guard hook; reads")
	fmt.Fprintln(w, "             one hook event as JSON on stdin, emits a decision on stdout")
}

func agentUsageErr() error {
	agentUsage(os.Stderr)
	return fmt.Errorf("agent: a subcommand is required (try: install)")
}

// agentInstallCmd writes the embedded skills for the named platform into dir
// (default CWD). Unknown platforms error explicitly rather than silently doing
// nothing, so demand is visible.
func agentInstallCmd(ctx context.Context, args []string) error {
	fset := flag.NewFlagSet("agent install", flag.ContinueOnError)
	dir := fset.String("dir", ".", "Repo directory to install into")
	force := fset.Bool("force", false, "Overwrite existing installed skill files")
	hooks := fset.Bool("hooks", true, "For claude: merge the PreToolUse guard hook into .claude/settings.json")
	fset.Usage = func() { agentUsage(os.Stderr) }
	if err := fset.Parse(args); err != nil {
		return err
	}
	rest := fset.Args()
	if len(rest) == 0 {
		agentUsage(os.Stderr)
		return fmt.Errorf("agent install: a platform is required (supported: %s)", strings.Join(platformNames(), ", "))
	}
	// The platform is the first positional. Re-parse the tail so a flag written after
	// it (agent install claude --force) is honored, not only flags written before.
	// A second positional is rejected below, so the re-parse only ever sees flags.
	platform := rest[0]
	if err := fset.Parse(rest[1:]); err != nil {
		return err
	}
	if extra := fset.Args(); len(extra) > 0 {
		return fmt.Errorf("agent install: one platform at a time, unexpected %q", extra[0])
	}

	var written []string
	var err error
	switch {
	case platform == agentsMDPlatform:
		written, err = installAgentsSection(*dir)
	case skillDirPlatforms[platform] != "":
		written, err = installSkillTree(*dir, skillDirPlatforms[platform], *force)
		if err == nil && platform == "claude" && *hooks {
			var hookWritten []string
			hookWritten, err = installClaudeHooks(*dir)
			written = append(written, hookWritten...)
		}
	default:
		return fmt.Errorf("agent install: platform %q is not supported yet (supported: %s); file an issue to request it",
			platform, strings.Join(platformNames(), ", "))
	}
	if err != nil {
		return err
	}
	for _, p := range written {
		slog.InfoContext(ctx, "agent install: wrote", slog.String("path", p))
	}
	printAgentInstallNextSteps(written)
	return nil
}

// installSkillTree copies the embedded skills/ tree into <dir>/<dest>/, stamping
// each markdown file with a generated-by footer. It refuses to overwrite an
// existing file unless force is set, returning the paths it wrote (repo-relative).
func installSkillTree(dir, dest string, force bool) ([]string, error) {
	var written []string
	err := fs.WalkDir(skillFS, "skills", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(p, "skills/") // e.g. "magus-query/SKILL.md"
		outPath := filepath.Join(dir, dest, rel)
		if !force {
			if _, err := os.Stat(outPath); err == nil {
				return fmt.Errorf("agent install: %s already exists (use --force to overwrite)", filepath.Join(dest, rel))
			}
		}
		body, err := skillFS.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(outPath, stampSkill(body), 0o644); err != nil {
			return fmt.Errorf("agent install: write %s: %w", outPath, err)
		}
		written = append(written, filepath.Join(dest, rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return written, nil
}

// agentsSectionMarkers delimit the managed magus block in AGENTS.md. The begin
// marker carries the version stamp (same fields as the skill footer) so the
// drift check reads it the same way. Everything between the markers is owned by
// magus and replaced wholesale on re-install; bytes outside them are never touched.
var (
	agentsSectionBegin = fmt.Sprintf(
		"<!-- magus:skills:begin generated by: magus agent install; agent-skill-version: %d; knowledge-schema-version: %d; do not edit, re-run to update -->",
		agentSkillVersion, types.KnowledgeSchemaVersion)
	agentsSectionEnd = "<!-- magus:skills:end -->"
	agentsSectionRe  = regexp.MustCompile(`(?s)<!-- magus:skills:begin .*?-->.*?<!-- magus:skills:end -->`)
)

// installAgentsSection writes (or replaces) the managed magus section in
// <dir>/AGENTS.md. Idempotent by construction - the section is regenerated in
// place - so it takes no force flag. A missing AGENTS.md is created holding just
// the section.
func installAgentsSection(dir string) ([]string, error) {
	path := filepath.Join(dir, "AGENTS.md")
	block := agentsSectionBegin + "\n\n" + strings.TrimSpace(agentsSection) + "\n\n" + agentsSectionEnd
	existing, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		if werr := os.WriteFile(path, []byte(block+"\n"), 0o644); werr != nil {
			return nil, fmt.Errorf("agent install: write %s: %w", path, werr)
		}
	case err != nil:
		return nil, fmt.Errorf("agent install: read %s: %w", path, err)
	case agentsSectionRe.Match(existing):
		out := agentsSectionRe.ReplaceAll(existing, []byte(block))
		if werr := os.WriteFile(path, out, 0o644); werr != nil {
			return nil, fmt.Errorf("agent install: write %s: %w", path, werr)
		}
	default:
		out := strings.TrimRight(string(existing), "\n") + "\n\n" + block + "\n"
		if werr := os.WriteFile(path, []byte(out), 0o644); werr != nil {
			return nil, fmt.Errorf("agent install: write %s: %w", path, werr)
		}
	}
	return []string{"AGENTS.md"}, nil
}

// skillFooter is the generated-by marker appended to every installed skill file.
// It is a greppable, parseable line a drift check reads to compare the installed
// version against the running binary's; the "do not edit" note steers humans to
// re-run install rather than hand-edit a generated file.
var skillFooter = fmt.Sprintf(
	"\n<!-- generated by: magus agent install; agent-skill-version: %d; knowledge-schema-version: %d; do not edit, re-run to update -->\n",
	agentSkillVersion, types.KnowledgeSchemaVersion)

// skillLicense is the SPDX identifier injected into every installed skill's
// provenance; it tracks the repository's LICENSE.
const skillLicense = "GPL-3.0-or-later"

// skillProvenance is the machine-owned frontmatter magus injects into every
// installed skill: the Agent Skills open-standard fields (license, compatibility)
// plus a metadata map carrying the same version counters as the footer, so a host
// that reads YAML frontmatter but not the trailing comment still sees the source
// and version. Source SKILL.md files carry only name/description (and hand-authored
// allowed-tools); these fields are added at install time, keeping magus the single
// source of truth for them across every platform.
var skillProvenance = fmt.Sprintf(
	"license: %s\ncompatibility: any-agent\nmetadata:\n  source: magus\n  agent-skill-version: %d\n  knowledge-schema-version: %d\n",
	skillLicense, agentSkillVersion, types.KnowledgeSchemaVersion)

// stampSkill injects provenance frontmatter and appends the footer to a skill
// file's content. The footer is a trailing HTML comment; the provenance goes just
// inside the leading YAML frontmatter the Agent Skills spec requires. The footer
// begins with its own newline, so it sits one blank line below the body -
// deliberate, for readability in the rendered file.
func stampSkill(body []byte) []byte {
	body = injectSkillProvenance(body)
	return append([]byte(strings.TrimRight(string(body), "\n")+"\n"), skillFooter...)
}

// injectSkillProvenance inserts skillProvenance immediately before the closing ---
// of the leading YAML frontmatter. Insertion is textual, not a YAML re-marshal, so
// the hand-authored name/description/allowed-tools stay byte-for-byte. A file with
// no frontmatter is returned unchanged (the footer still stamps it).
func injectSkillProvenance(body []byte) []byte {
	s := string(body)
	if !strings.HasPrefix(s, "---\n") {
		return body
	}
	rel := strings.Index(s[len("---\n"):], "\n---")
	if rel < 0 {
		return body
	}
	closeAt := len("---\n") + rel + 1 // start of the closing "---" line
	return []byte(s[:closeAt] + skillProvenance + s[closeAt:])
}

// footerVersionRe pulls the two versions out of an installed skill's footer (or
// the AGENTS.md begin marker - same fields), so a drift check can compare them
// against the running binary without a byte diff.
var footerVersionRe = regexp.MustCompile(`agent-skill-version: (\d+); knowledge-schema-version: (\d+)`)

// anchorSkillRel is the skill file whose footer anchors the drift check inside a
// skill-dir install: every install writes it, so its stamp speaks for the set.
const anchorSkillRel = "magus-query/SKILL.md"

// skillStatus is the verdict of checking one platform's install against this
// binary: whether it is present, and whether it has fallen behind (Stale). The
// happy value is {Installed: true, Stale: false}.
type skillStatus struct {
	Platform  string
	Installed bool // the install exists
	Stale     bool // it exists but its version predates the binary's
	Detail    string
}

// checkSkillStatuses inspects every supported platform's install under dir and
// returns one status per platform that has anything on disk, sorted by platform
// name. An empty slice means nothing is installed anywhere. It is the read half
// of the generated-by stamps: install writes the version, this tells an operator
// or CI when a re-install is due after a magus upgrade.
func checkSkillStatuses(dir string) []skillStatus {
	var out []skillStatus
	for platform, dest := range skillDirPlatforms {
		path := filepath.Join(dir, dest, anchorSkillRel)
		body, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			// Present but unreadable (permissions, IO) is a real problem, not "absent":
			// report it as drift so a --strict CI gate fails instead of passing green.
			out = append(out, skillStatus{Platform: platform, Installed: true, Stale: true,
				Detail: "cannot read installed skill: " + err.Error()})
			continue
		}
		out = append(out, gradeStamp(platform, string(body)))
	}
	if body, err := os.ReadFile(filepath.Join(dir, "AGENTS.md")); err == nil {
		if section := agentsSectionRe.Find(body); section != nil {
			out = append(out, gradeStamp(agentsMDPlatform, string(section)))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Platform < out[j].Platform })
	return out
}

// gradeStamp grades one install's stamped versions against the running binary.
func gradeStamp(platform, body string) skillStatus {
	m := footerVersionRe.FindStringSubmatch(body)
	if m == nil {
		return skillStatus{Platform: platform, Installed: true, Stale: true,
			Detail: "installed skill has no version stamp; re-run: magus agent install " + platform + " --force"}
	}
	// The regex captured \d+ for both groups, so Atoi cannot fail here.
	skillVer, _ := strconv.Atoi(m[1])
	schemaVer, _ := strconv.Atoi(m[2])
	if skillVer < agentSkillVersion || schemaVer < types.KnowledgeSchemaVersion {
		return skillStatus{Platform: platform, Installed: true, Stale: true,
			Detail: fmt.Sprintf("stale (skill v%d/schema v%d; binary v%d/schema v%d); re-run: magus agent install %s --force",
				skillVer, schemaVer, agentSkillVersion, types.KnowledgeSchemaVersion, platform)}
	}
	return skillStatus{Platform: platform, Installed: true,
		Detail: fmt.Sprintf("up to date (skill v%d, schema v%d)", skillVer, schemaVer)}
}

// printAgentInstallNextSteps prints an actionable hint after install, gated on
// interactive.Enabled() so MAGUS_HINTS_ENABLED=false silences it.
func printAgentInstallNextSteps(written []string) {
	if !interactive.Enabled() || len(written) == 0 {
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
		strings.TrimSpace(agentsSection) + "\n"
}

// agentSampleCmd prints agentSampleDoc to stdout. It never writes a file: an
// AGENTS.md is the developer's to own, and clobbering an existing one would be the
// opposite of helpful.
func agentSampleCmd() error {
	fmt.Fprint(os.Stdout, agentSampleDoc())
	return nil
}

// agentHookCommand is the shell line installed into .claude/settings.json as a
// PreToolUse hook on the Bash tool. It fails open: when magus is not on PATH the
// hook exits 0 and the tool call proceeds unguarded, rather than erroring on
// every Bash call in a repo that has the config but not the binary.
const agentHookCommand = "command -v magus >/dev/null 2>&1 || exit 0; exec magus agent hook"

// installClaudeHooks merges the magus PreToolUse guard hook into
// <dir>/.claude/settings.json, preserving every other key. Idempotent: an entry
// whose command invokes `magus agent hook` is replaced in place, never
// duplicated. A settings file that fails to parse is left untouched (refusing
// beats clobbering a hand-edited config).
func installClaudeHooks(dir string) ([]string, error) {
	path := filepath.Join(dir, ".claude", "settings.json")
	settings := map[string]any{}
	if body, err := os.ReadFile(path); err == nil {
		if jerr := json.Unmarshal(body, &settings); jerr != nil {
			return nil, fmt.Errorf("agent install: %s is not valid JSON, refusing to touch it: %w", path, jerr)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("agent install: read %s: %w", path, err)
	}

	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	pre, _ := hooks["PreToolUse"].([]any)

	entry := map[string]any{
		"matcher": "Bash",
		"hooks": []any{map[string]any{
			"type":    "command",
			"command": agentHookCommand,
			"timeout": 10,
		}},
	}
	replaced := false
	for i, e := range pre {
		if preToolUseEntryIsMagus(e) {
			pre[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		pre = append(pre, entry)
	}
	hooks["PreToolUse"] = pre

	// A plain Marshal would HTML-escape the > and & in the hook command into
	// > escapes a human then has to read in their settings file.
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(settings); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(buf.String()), 0o644); err != nil {
		return nil, fmt.Errorf("agent install: write %s: %w", path, err)
	}
	return []string{filepath.Join(".claude", "settings.json")}, nil
}

// preToolUseEntryIsMagus reports whether a PreToolUse entry is the one magus
// manages: any of its command hooks invokes `magus agent hook`.
func preToolUseEntryIsMagus(e any) bool {
	m, ok := e.(map[string]any)
	if !ok {
		return false
	}
	inner, _ := m["hooks"].([]any)
	for _, h := range inner {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if cmd, _ := hm["command"].(string); strings.Contains(cmd, "magus agent hook") {
			return true
		}
	}
	return false
}

// agentHookCmd implements `magus agent hook`: plumbing invoked by the installed
// Claude Code PreToolUse hook. It reads one hook event as JSON on stdin and
// emits a decision on stdout. Anything unrecognized - a malformed event, a
// different event or tool, a command no rule matches - produces no output and
// exit 0, which the hook contract reads as "proceed": a guard must fail open or
// it turns every Bash call into a hard failure.
func agentHookCmd(in io.Reader, out io.Writer) error {
	var ev struct {
		HookEventName string `json:"hook_event_name"`
		ToolName      string `json:"tool_name"`
		ToolInput     struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	if err := json.NewDecoder(in).Decode(&ev); err != nil {
		return nil
	}
	if ev.HookEventName != "PreToolUse" || ev.ToolName != "Bash" {
		return nil
	}
	v := evaluateBashGuard(ev.ToolInput.Command)
	hso := map[string]any{"hookEventName": "PreToolUse"}
	switch {
	case v.Deny != "":
		hso["permissionDecision"] = "deny"
		hso["permissionDecisionReason"] = v.Deny
	case v.Context != "":
		hso["additionalContext"] = v.Context
	default:
		return nil
	}
	return json.NewEncoder(out).Encode(map[string]any{"hookSpecificOutput": hso})
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
