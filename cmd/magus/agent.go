package main

import (
	"context"
	"embed"
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
//	v3: teach the refs verb / magus_refs (SCIP symbol def+references)
//	v4: teach the * wildcard in the query grammar
//	v5: teach the graph diff verb (PR blast-radius against a baseline export)
//	v6: teach the opt-in @vcs git-history attrs on file nodes
//	v7: purpose-named skill set (magus-query, magus-run, magus-vcs,
//	    magus-architecture, magus-memory); multi-platform install (claude/
//	    opencode/agents skill dirs, AGENTS.md section for codex); query-first
//	    fast path; describe-file triage; durable magus_memory workflow
const agentSkillVersion = 7

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
// Today the one verb is `install <platform>`, which writes the embedded skill into
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
	case "-h", "--help", "help":
		agentUsage(os.Stderr)
		return nil
	default:
		return fmt.Errorf("agent: unknown subcommand %q (try: install)", args[0])
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
	fmt.Fprintln(w, "magus-architecture, magus-memory); platforms differ only in where they")
	fmt.Fprintln(w, "discover them.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Platforms:")
	fmt.Fprintln(w, "  claude     write .claude/skills/ (Claude Code)")
	fmt.Fprintln(w, "  opencode   write .opencode/skills/ (OpenCode)")
	fmt.Fprintln(w, "  agents     write .agents/skills/ (Agent Skills spec generic location)")
	fmt.Fprintln(w, "  codex      write a managed magus section into AGENTS.md (Codex and")
	fmt.Fprintln(w, "             any other AGENTS.md-reading agent)")
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

// stampSkill appends the footer to a skill file's content (a trailing HTML comment
// leaves the leading YAML frontmatter the Agent Skills spec requires untouched). The
// footer begins with its own newline, so it sits one blank line below the body -
// deliberate, for readability in the rendered file.
func stampSkill(body []byte) []byte {
	return append([]byte(strings.TrimRight(string(body), "\n")+"\n"), skillFooter...)
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
}
