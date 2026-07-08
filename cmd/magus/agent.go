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
//go:embed skills
var skillFS embed.FS

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
const agentSkillVersion = 6

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
	fmt.Fprintln(w, "Install the magus agent skill into the current repo so an agent knows")
	fmt.Fprintln(w, "how to use the knowledge graph (query/explain/path/stats) instead of grepping.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Platforms:")
	fmt.Fprintln(w, "  claude   write .claude/skills/magus/ (Claude Code Agent Skills)")
}

func agentUsageErr() error {
	agentUsage(os.Stderr)
	return fmt.Errorf("agent: a subcommand is required (try: install)")
}

// agentInstallCmd writes the embedded skill for the named platform into dir
// (default CWD). Only claude is supported today; other platforms error explicitly
// rather than silently doing nothing, so demand is visible.
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
		return fmt.Errorf("agent install: a platform is required (supported: claude)")
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
	if platform != "claude" {
		return fmt.Errorf("agent install: platform %q is not supported yet (supported: claude); file an issue to request it", platform)
	}

	written, err := installClaudeSkill(*dir, *force)
	if err != nil {
		return err
	}
	for _, p := range written {
		slog.InfoContext(ctx, "agent install: wrote skill file", slog.String("path", p))
	}
	printAgentInstallNextSteps(*dir, written)
	return nil
}

// installClaudeSkill copies the embedded skills/ tree into <dir>/.claude/skills/,
// stamping each markdown file with a generated-by footer. It refuses to overwrite
// an existing file unless force is set, returning the paths it wrote (repo-relative).
func installClaudeSkill(dir string, force bool) ([]string, error) {
	const dest = ".claude/skills"
	var written []string
	err := fs.WalkDir(skillFS, "skills", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(p, "skills/") // e.g. "magus/SKILL.md"
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

// footerVersionRe pulls the two versions out of an installed skill's footer, so a
// drift check can compare them against the running binary without a byte diff.
var footerVersionRe = regexp.MustCompile(`agent-skill-version: (\d+); knowledge-schema-version: (\d+)`)

// installedSkillPath is where a Claude install writes the skill, relative to a repo.
const installedSkillPath = ".claude/skills/magus/SKILL.md"

// skillStatus is the verdict of checking an installed skill against this binary:
// whether it is present, and whether it has fallen behind (Stale). The happy value
// is {Installed: true, Stale: false}.
type skillStatus struct {
	Installed bool // the skill file exists
	Stale     bool // it exists but its version predates the binary's
	Detail    string
}

// checkSkillStatus reads the installed skill under dir and reports whether it is
// missing, current, or stale (its stamped versions older than the binary's). It is
// the read half of the generated-by footer: install stamps the version, this tells
// an operator or CI when a re-install is due after a magus upgrade.
func checkSkillStatus(dir string) skillStatus {
	body, err := os.ReadFile(filepath.Join(dir, installedSkillPath))
	if err != nil {
		if os.IsNotExist(err) {
			return skillStatus{Detail: "not installed (run: magus agent install claude)"}
		}
		// Present but unreadable (permissions, IO) is a real problem, not "absent":
		// report it as drift so a --strict CI gate fails instead of passing green.
		return skillStatus{Installed: true, Stale: true, Detail: "cannot read installed skill: " + err.Error()}
	}
	m := footerVersionRe.FindStringSubmatch(string(body))
	if m == nil {
		return skillStatus{Installed: true, Stale: true, Detail: "installed skill has no version footer; re-run: magus agent install claude --force"}
	}
	// The regex captured \d+ for both groups, so Atoi cannot fail here.
	skillVer, _ := strconv.Atoi(m[1])
	schemaVer, _ := strconv.Atoi(m[2])
	if skillVer < agentSkillVersion || schemaVer < types.KnowledgeSchemaVersion {
		return skillStatus{
			Installed: true, Stale: true,
			Detail: fmt.Sprintf("installed skill is stale (skill v%d/schema v%d; binary v%d/schema v%d); re-run: magus agent install claude --force",
				skillVer, schemaVer, agentSkillVersion, types.KnowledgeSchemaVersion),
		}
	}
	return skillStatus{Installed: true, Detail: fmt.Sprintf("up to date (skill v%d, schema v%d)", skillVer, schemaVer)}
}

// printAgentInstallNextSteps prints an actionable hint after install, gated on
// interactive.Enabled() so MAGUS_HINTS_ENABLED=false silences it.
func printAgentInstallNextSteps(dir string, written []string) {
	if !interactive.Enabled() || len(written) == 0 {
		return
	}
	interactive.Emit(os.Stderr, fmt.Sprintf("installed the magus skill (%d file(s)) under %s", len(written), filepath.Join(dir, ".claude/skills")))
	interactive.Emit(os.Stderr, "commit .claude/skills/magus/ so your team and agents share it")
	interactive.Emit(os.Stderr, "the skill points at MAGUS.md's routing table:  magus describe graph -o markdown")
}
