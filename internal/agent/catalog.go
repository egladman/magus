// Package agent owns Magus's provider-neutral agent-skill artifact. Command
// packages supply embedded source files; this package renders, installs, and
// verifies the generated surface without knowing about a particular CLI host.
package agent

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SkillVersion changes when the installed skill contract changes. It is part
// of the generated provenance and lets verification explain stale installs.
const SkillVersion = 19

const skillLicense = "GPL-3.0-or-later"

const anchorSkillRel = "magus-query/SKILL.md"

var wellKnownSkillDirs = []string{".agents/skills", ".claude/skills", ".opencode/skills"}

// WellKnownSkillDirs returns the conventional locations verification probes.
// Installation remains explicit: callers must name every destination to write.
func WellKnownSkillDirs() []string { return append([]string(nil), wellKnownSkillDirs...) }

// AgentSkill is Magus's provider-neutral skill contract. Renderers own provider
// metadata and file shape; the Markdown body only explains the workflow.
type AgentSkill struct {
	Name        string
	Description string
	Body        string
}

// Variant selects which permutation of a skill body to render.
//
// BOTH PERMUTATIONS ARE CURATED, and that is the whole design. The simple one is
// not a summary, a truncation, or a model-generated paraphrase: there is exactly
// one human-written body per skill, and its author brackets the spans that only
// the full permutation keeps. So the two can never come to describe different
// behaviour, they share one content digest, and they version together - which is
// the property a second hand-maintained file could not give.
//
// The reason to offer a shorter one at all: a skill is a bet about what the reader
// cannot infer, and that bet ages. Models keep getting better at inferring the
// why, so the rationale that earns its context today is the same text that is
// dead weight in a year. Rather than let the skills quietly become bricks, the
// choice is a flag - and re-asking "does this still earn its context?" is the
// audit, not a rewrite.
type Variant int

const (
	// VariantFull is the default: the imperative steps plus the rationale that
	// says why each one is the right move and what goes wrong otherwise.
	VariantFull Variant = iota
	// VariantSimple keeps the imperative steps and withholds the rationale, for a
	// capable reader that would rather spend the context on the task. It is a bet
	// ON the reader, not a lossy compression - which is why the split is a
	// judgement an author records, and why anything a step cannot survive losing
	// belongs in the unmarked core instead.
	VariantSimple
)

func (v Variant) String() string {
	if v == VariantSimple {
		return "simple"
	}
	return "full"
}

// VariantOf maps a --simple boolean to a Variant, so the CLI does not spell the
// conditional at every call site.
func VariantOf(simple bool) Variant {
	if simple {
		return VariantSimple
	}
	return VariantFull
}

// whyOpen and whyClose bracket prose that only [VariantFull] keeps.
//
// They are HTML comments so a marked body stays valid Markdown that renders
// identically either way - the source is readable, and a skill file opened in any
// viewer shows no scaffolding. One pair covers both granularities: a whole
// paragraph, or a trailing clause inside a numbered step, because the span is
// taken verbatim between the markers regardless of newlines. One rule, not two.
//
// The name says the intent rather than the mechanism. What a simple skill drops
// is the WHY; what both keep is the WHAT. An author deciding where the marker
// goes is answering "would a capable reader still do the right thing without this
// sentence?", and that question is the curation.
const (
	whyOpen  = "<!-- why -->"
	whyClose = "<!-- /why -->"
)

// applyVariant renders body for v: [VariantSimple] removes each marked span,
// [VariantFull] keeps the prose and removes only the markers themselves, so no
// scaffolding reaches an installed file either way.
//
// Unbalanced markers are an ERROR, never a best guess. A generator that silently
// mis-elides ships a skill missing a step, and a missing step in an instruction
// file is indistinguishable from an instruction not to do it.
func applyVariant(name, body string, v Variant) (string, error) {
	var b strings.Builder
	rest := body
	for {
		open := strings.Index(rest, whyOpen)
		if open < 0 {
			if strings.Contains(rest, whyClose) {
				return "", fmt.Errorf("skill %q: %s with no matching %s", name, whyClose, whyOpen)
			}
			b.WriteString(rest)
			break
		}
		after := rest[open+len(whyOpen):]
		close := strings.Index(after, whyClose)
		if close < 0 {
			return "", fmt.Errorf("skill %q: %s with no matching %s", name, whyOpen, whyClose)
		}
		if inner := after[:close]; strings.Contains(inner, whyOpen) {
			return "", fmt.Errorf("skill %q: nested %s", name, whyOpen)
		}
		b.WriteString(rest[:open])
		if v == VariantFull {
			b.WriteString(after[:close])
		}
		rest = after[close+len(whyClose):]
		if v == VariantSimple {
			trimSeam(&b, rest)
		}
	}
	return tidyBlankLines(b.String()), nil
}

// trimSeam drops the space left dangling at an elision boundary, when the removed
// span sat between a space and the punctuation that closed the sentence: "done - a
// long reason<!-- /why -->." would otherwise render as "done ." with a floating space.
//
// It looks at exactly the two characters either side of the cut, and nowhere else.
// A global " ." -> "." pass is the obvious shortcut and it is wrong: it silently
// rewrote `git checkout .` to `git checkout.` in the middle of a command the skill
// tells the reader never to run - corrupting content that was never elided at all.
func trimSeam(b *strings.Builder, rest string) {
	if b.Len() == 0 || rest == "" {
		return
	}
	if !strings.ContainsRune(".,;:)!?", rune(rest[0])) {
		return
	}
	s := b.String()
	if !strings.HasSuffix(s, " ") {
		return
	}
	b.Reset()
	b.WriteString(strings.TrimRight(s, " "))
}

// tidyBlankLines collapses the blank-line runs a dropped paragraph leaves behind
// and strips trailing whitespace, so an elided body is still well-formed Markdown.
//
// It does not rewrap prose. A paragraph left with ragged line lengths renders
// identically - Markdown folds single newlines inside a paragraph into spaces - and
// a rewrapper would have to understand fenced code, tables and list indentation to
// avoid breaking them, which is a lot of machinery to buy nothing the reader sees.
func tidyBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if l == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// Status is the verification verdict for one installed skill location.
type Status struct {
	Location  string
	Installed bool
	Stale     bool
	Detail    string
}

type skillSource struct {
	name        string
	description string
	bodyPath    string
}

var skillSources = []skillSource{
	{name: "magus-architecture", description: "Ground refactoring and structure proposals in the magus knowledge graph instead of intuition. Use when suggesting directory structure, package layout, or module boundaries, when deciding where new code belongs, when assessing the blast radius or risk of a refactor, or when asked where a magus workspace's coupling and churn concentrate.", bodyPath: "skills/magus-architecture/SKILL.md"},
	{name: "magus-buzz", description: "Write and run Buzz, the language magusfiles, spells, and `magus buzz` scripts are written in. Use when writing or debugging a magusfile target, a spell, or a .buzz file, and when a one-off script is needed in a magus workspace - Buzz is already installed with the whole magus host surface (fs, http, json, yaml, template, vcs, ...), so it needs no dependency install. Also use when Buzz syntax surprises you: namespace access is a backslash, object literals use `=`, and `magus buzz` runs upstream-strict (no top-level control flow, every argument after the first must be labeled).", bodyPath: "skills/magus-buzz/SKILL.md"},
	{name: "magus-changes", description: "Summarize what merged, changed, or landed recently in a magus workspace. Use for questions such as \"what's been merged lately?\", \"what features landed recently?\", \"catch me up since last week\", or \"what changed in this monorepo?\" Ground each conclusion in VCS history plus magus project and knowledge-graph evidence; do not infer features from commit subjects alone.", bodyPath: "skills/magus-changes/SKILL.md"},
	{name: "magus-docs", description: "Traverse magus's own documentation to answer a \"how does magus do X / what does Y mean / where is Z documented\" question, instead of guessing an answer or a URL. Use when you need authoritative magus behavior (a CLI flag, a spell op, a diagnostic code, a config key, a stdlib module) and the workspace graph cannot give it. Do NOT use for facts about THIS workspace (use magus-query) or to run work (use magus-run).", bodyPath: "skills/magus-docs/SKILL.md"},
	{name: "magus-memory", description: "Maintain a user-owned handoff journal through magus_memory or `magus memory`: named decisions, plans, and pointers that survive worktrees and sessions. It is not automatic agent memory; add an entry only when a later person needs to reopen the linked graph/query/output/doc evidence. Verify malformed, stale, and broken-linked entries before relying on them.", bodyPath: "skills/magus-memory/SKILL.md"},
	{name: "magus-query", description: "Query the magus knowledge graph to find and relate entities (projects, targets, spells, ops, charms, modules, diagnostics, docs). Use INSTEAD of Grep or Glob in a repo with magusfile.buzz whenever the question is what exists, what depends on what, where something is used, or how two entities relate - a graph answer is verified against declared sources, a grep hit is a guess.", bodyPath: "skills/magus-query/SKILL.md"},
	{name: "magus-run", description: "Run builds, tests, lints, and codegen through magus targets. Use BEFORE typing go test, go build, npm test, npx, eslint, prettier, pytest, tsc, cargo, or any other raw language tool in a repo with magusfile.buzz at the root - a target covers the work, and the raw tool bypasses the cache, the sandbox, and affected tracking. Also use when a magus target fails and you need its captured output, and for the final pre-commit gate (magus affected ci).", bodyPath: "skills/magus-run/SKILL.md"},
	{name: "magus-vcs", description: "Safe git operations in a magus workspace (any repo with magusfile.buzz at the root). Use IMMEDIATELY before git commit, git add, git stash, git reset, git checkout, or git clean, and when reading git status or a diff - especially one touching MAGUS.md, gen/ trees, lockfiles, or other generated files. Classifies every changed path as generated output vs source (magus describe file), gives the commit checklist, and settles merge conflicts in generated files by regenerating. Do NOT stash or reset the whole tree to verify a build; load this skill first.", bodyPath: "skills/magus-vcs/SKILL.md"},
}

// Catalog binds the skill source assets embedded by the application to the
// schema version of that application. It contains no CLI parsing or rendering.
type Catalog struct {
	sourceFS      fs.FS
	agentsSection string
	schemaVersion int
	contentDigest string
}

func NewCatalog(sourceFS fs.FS, agentsSection string, schemaVersion int) *Catalog {
	c := &Catalog{sourceFS: sourceFS, agentsSection: agentsSection, schemaVersion: schemaVersion}
	c.contentDigest = c.computeContentDigest()
	return c
}

// EmbeddedSkills returns every embedded skill rendered for v, in name order.
func (c *Catalog) EmbeddedSkills(v Variant) ([]AgentSkill, error) {
	sources := append([]skillSource(nil), skillSources...)
	sort.Slice(sources, func(i, j int) bool { return sources[i].name < sources[j].name })
	skills := make([]AgentSkill, 0, len(sources))
	for _, source := range sources {
		body, err := fs.ReadFile(c.sourceFS, source.bodyPath)
		if err != nil {
			return nil, err
		}
		rendered, err := applyVariant(source.name, strings.TrimSpace(string(body)), v)
		if err != nil {
			return nil, err
		}
		skills = append(skills, AgentSkill{Name: source.name, Description: source.description, Body: rendered})
	}
	return skills, nil
}

// RenderSkill renders the open Agent Skills format. Other provider renderers
// can consume AgentSkill without duplicating the source definitions.
func (c *Catalog) RenderSkill(skill AgentSkill) []byte {
	return []byte(fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", skill.Name, strconv.Quote(skill.Description), skill.Body))
}

// SkillBytes returns the rendered+stamped bytes for one named skill.
// Pure rendering: callers decide what to do with the bytes (write to a
// file, embed in a tar, hash, log).
func (c *Catalog) SkillBytes(name string, v Variant) ([]byte, error) {
	skills, err := c.EmbeddedSkills(v)
	if err != nil {
		return nil, err
	}
	for _, skill := range skills {
		if skill.Name == name {
			return c.StampSkill(c.RenderSkill(skill), v), nil
		}
	}
	return nil, fmt.Errorf("unknown skill %q", name)
}

// SkillNames returns the embedded skill names in deterministic order.
func (c *Catalog) SkillNames() ([]string, error) {
	skills, err := c.EmbeddedSkills(VariantFull)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(skills))
	for _, skill := range skills {
		names = append(names, skill.Name)
	}
	sort.Strings(names)
	return names, nil
}

// SkillTar returns a tar archive of every embedded skill at the path
// `<dest>/<skill-name>/SKILL.md`. The archive is reproducible: tar headers
// carry a fixed mtime and deterministic mode bits so byte-equal output is
// possible when the binary and skill content are unchanged. Piping the
// result to `tar -xf - -C <dir>` is the supported way to install skills
// outside the workspace root - the shell sees the command, the sandbox sees
// it, and the user gets to choose the destination.
func (c *Catalog) SkillTar(dest string, v Variant) ([]byte, error) {
	skills, err := c.EmbeddedSkills(v)
	if err != nil {
		return nil, err
	}
	if dest == "" {
		dest = "."
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	epoch := time.Unix(0, 0).UTC()
	for _, skill := range skills {
		body := c.StampSkill(c.RenderSkill(skill), v)
		hdr := &tar.Header{
			Name:    filepath.ToSlash(filepath.Join(dest, skill.Name, "SKILL.md")),
			Mode:    0o644,
			Size:    int64(len(body)),
			ModTime: epoch,
			Uname:   "",
			Gname:   "",
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("agent install: tar header: %w", err)
		}
		if _, err := tw.Write(body); err != nil {
			return nil, fmt.Errorf("agent install: tar body: %w", err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("agent install: tar close: %w", err)
	}
	return buf.Bytes(), nil
}

// WriteSkillTree renders the standard Agent Skills format into <dir>/<dest>.
// The destination must be a path relative to <dir>; absolute paths are
// refused so magus never silently writes outside the working tree. The
// caller is responsible for that guard at the CLI surface; this method
// enforces it for safety.
func (c *Catalog) WriteSkillTree(dir, dest string, force bool, v Variant) ([]string, error) {
	if filepath.IsAbs(dest) || strings.HasPrefix(dest, "~") {
		return nil, fmt.Errorf("agent install: destination %q is outside the working tree; pass --global or use --tar | tar -xf - -C <dir>", dest)
	}
	skills, err := c.EmbeddedSkills(v)
	if err != nil {
		return nil, err
	}
	var written []string
	for _, skill := range skills {
		rel := filepath.Join(skill.Name, "SKILL.md")
		outPath := filepath.Join(dir, dest, rel)
		if !force {
			if _, err := os.Stat(outPath); err == nil {
				return nil, fmt.Errorf("agent install: %s already exists (use --force to overwrite)", filepath.Join(dest, rel))
			} else if !os.IsNotExist(err) {
				return nil, fmt.Errorf("agent install: stat %s: %w", outPath, err)
			}
		}
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(outPath, c.StampSkill(c.RenderSkill(skill), v), 0o644); err != nil {
			return nil, fmt.Errorf("agent install: write %s: %w", outPath, err)
		}
		written = append(written, filepath.Join(dest, rel))
	}
	return written, nil
}

func (c *Catalog) agentsSectionBegin() string {
	return fmt.Sprintf("<!-- magus:skills:begin generated by: magus agent install; agent-skill-version: %d; knowledge-schema-version: %d; skill-content: %s; do not edit, re-run to update -->", SkillVersion, c.schemaVersion, c.contentDigest)
}

const agentsSectionEnd = "<!-- magus:skills:end -->"

var agentsSectionRe = regexp.MustCompile(`(?s)<!-- magus:skills:begin .*?-->.*?<!-- magus:skills:end -->`)

// RenderAgentsSection returns the merged AGENTS.md content for <dir>: the
// managed magus block placed between its begin/end markers, with everything
// outside the markers preserved from any existing file. If no file exists
// at <dir>/AGENTS.md, the result starts with a `# AGENTS.md\n\n` header.
// Pure rendering: callers decide what to do with the bytes.
func (c *Catalog) RenderAgentsSection(dir string) ([]byte, error) {
	path := filepath.Join(dir, "AGENTS.md")
	block := c.agentsSectionBegin() + "\n\n" + strings.TrimSpace(c.agentsSection) + "\n\n" + agentsSectionEnd
	existing, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		return []byte("# AGENTS.md\n\n" + block + "\n"), nil
	case err != nil:
		return nil, fmt.Errorf("agent install: read %s: %w", path, err)
	case agentsSectionRe.Match(existing):
		out := agentsSectionRe.ReplaceAll(existing, []byte(block))
		if strings.HasPrefix(strings.TrimSpace(string(out)), "<!-- magus:skills:begin") {
			out = []byte("# AGENTS.md\n\n" + string(out))
		}
		return out, nil
	default:
		out := strings.TrimRight(string(existing), "\n") + "\n\n" + block + "\n"
		return []byte(out), nil
	}
}

// AgentsSectionTar returns a tar archive containing one file,
// `AGENTS.md`, with the merged content of the managed magus block for <dir>.
// Use it the same way as SkillTar: `magus agent install-agents-md
// --tar --dir . | tar -xf - -C <path>`.
func (c *Catalog) AgentsSectionTar(dir string) ([]byte, error) {
	body, err := c.RenderAgentsSection(dir)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Name:    "AGENTS.md",
		Mode:    0o644,
		Size:    int64(len(body)),
		ModTime: time.Unix(0, 0).UTC(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return nil, fmt.Errorf("agent install: tar header: %w", err)
	}
	if _, err := tw.Write(body); err != nil {
		return nil, fmt.Errorf("agent install: tar body: %w", err)
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("agent install: tar close: %w", err)
	}
	return buf.Bytes(), nil
}

// WriteAgentsSection renders and writes the merged AGENTS.md content back
// to <dir>/AGENTS.md.
func (c *Catalog) WriteAgentsSection(dir string) ([]string, error) {
	body, err := c.RenderAgentsSection(dir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return nil, fmt.Errorf("agent install: write %s: %w", path, err)
	}
	return []string{"AGENTS.md"}, nil
}

func (c *Catalog) provenance(v Variant) string {
	return fmt.Sprintf("license: %s\ncompatibility: any-agent\nmetadata:\n  source: magus\n  agent-skill-version: %d\n  knowledge-schema-version: %d\n  skill-content: %s\n  skill-variant: %s\n", skillLicense, SkillVersion, c.schemaVersion, c.contentDigest, v)
}

func (c *Catalog) footer(v Variant) string {
	return fmt.Sprintf("\n<!-- generated by: magus agent install; agent-skill-version: %d; knowledge-schema-version: %d; skill-content: %s; skill-variant: %s; do not edit, re-run to update -->\n", SkillVersion, c.schemaVersion, c.contentDigest, v)
}

// StampSkill injects provenance frontmatter and appends a generated-by footer.
//
// The stamp names the variant but keeps the SOURCE content digest, deliberately:
// both permutations come from one body, so they must report the same digest and go
// stale together. A per-variant digest would let a simple install look current
// against a source its full sibling had already outgrown.
func (c *Catalog) StampSkill(body []byte, v Variant) []byte {
	body = c.injectProvenance(body, v)
	return append([]byte(strings.TrimRight(string(body), "\n")+"\n"), c.footer(v)...)
}

func (c *Catalog) injectProvenance(body []byte, v Variant) []byte {
	s := string(body)
	if !strings.HasPrefix(s, "---\n") {
		return body
	}
	rel := strings.Index(s[len("---\n"):], "\n---")
	if rel < 0 {
		return body
	}
	closeAt := len("---\n") + rel + 1
	return []byte(s[:closeAt] + c.provenance(v) + s[closeAt:])
}

func (c *Catalog) computeContentDigest() string {
	h := sha256.New()
	paths, err := c.skillSourceFiles()
	if err != nil {
		return "unreadable"
	}
	for _, p := range paths {
		body, err := fs.ReadFile(c.sourceFS, p)
		if err != nil {
			return "unreadable"
		}
		if _, err := fmt.Fprintf(h, "%d:%s\n", len(p), p); err != nil {
			return "unreadable"
		}
		if _, err := h.Write(body); err != nil {
			return "unreadable"
		}
	}
	for _, source := range skillSources {
		if _, err := fmt.Fprintf(h, "%s\n%s\n", source.name, source.description); err != nil {
			return "unreadable"
		}
	}
	if _, err := fmt.Fprintf(h, "%d:agents-section.md\n", len(c.agentsSection)); err != nil {
		return "unreadable"
	}
	if _, err := h.Write([]byte(c.agentsSection)); err != nil {
		return "unreadable"
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func (c *Catalog) skillSourceFiles() ([]string, error) {
	seen := make(map[string]bool, len(skillSources))
	paths := make([]string, 0, len(skillSources))
	for _, source := range skillSources {
		if source.name == "" || source.description == "" || source.bodyPath == "" {
			return nil, fmt.Errorf("invalid embedded skill definition")
		}
		if seen[source.name] {
			return nil, fmt.Errorf("duplicate embedded skill %q", source.name)
		}
		seen[source.name] = true
		if _, err := fs.Stat(c.sourceFS, source.bodyPath); err != nil {
			return nil, err
		}
		paths = append(paths, source.bodyPath)
	}
	sort.Strings(paths)
	return paths, nil
}

var footerVersionRe = regexp.MustCompile(`agent-skill-version: (\d+); knowledge-schema-version: (\d+)`)
var footerDigestRe = regexp.MustCompile(`skill-content: ([0-9a-f]+|unreadable)`)

// CheckStatuses inspects known skill locations plus AGENTS.md, returning only
// locations with a Magus install. The result order is deterministic.
func (c *Catalog) CheckStatuses(dir string) []Status {
	var out []Status
	for _, dest := range wellKnownSkillDirs {
		path := filepath.Join(dir, dest, anchorSkillRel)
		body, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			out = append(out, Status{Location: dest, Installed: true, Stale: true, Detail: "cannot read installed skill: " + err.Error()})
			continue
		}
		out = append(out, c.gradeStamp(dest, "magus agent install "+dest+" --force", string(body)))
	}
	if body, err := os.ReadFile(filepath.Join(dir, "AGENTS.md")); err == nil {
		if section := agentsSectionRe.Find(body); section != nil {
			// install-agents-md is a SUBCOMMAND, not a flag on install. The old string
			// named a flag that does not parse, so the one thing a stale stamp is
			// supposed to give you - the command that fixes it - failed three times
			// before the real name turned up in `magus agent install -h`.
			out = append(out, c.gradeStamp("AGENTS.md", "magus agent install-agents-md", string(section)))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Location < out[j].Location })
	return out
}

func (c *Catalog) gradeStamp(location, reinstall, body string) Status {
	m := footerVersionRe.FindStringSubmatch(body)
	if m == nil {
		return Status{Location: location, Installed: true, Stale: true, Detail: "installed skill has no version stamp; re-run: " + reinstall}
	}
	skillVersion, _ := strconv.Atoi(m[1])
	schemaVersion, _ := strconv.Atoi(m[2])
	if skillVersion < SkillVersion || schemaVersion < c.schemaVersion {
		return Status{Location: location, Installed: true, Stale: true, Detail: fmt.Sprintf("stale (skill v%d/schema v%d; binary v%d/schema v%d); re-run: %s", skillVersion, schemaVersion, SkillVersion, c.schemaVersion, reinstall)}
	}
	d := footerDigestRe.FindStringSubmatch(body)
	if d == nil {
		return Status{Location: location, Installed: true, Stale: true, Detail: "installed by a magus that predates the content fingerprint; re-run: " + reinstall}
	}
	if d[1] != c.contentDigest {
		return Status{Location: location, Installed: true, Stale: true, Detail: fmt.Sprintf("content differs from this binary's embedded skills (installed %s, binary %s); re-run: %s", d[1], c.contentDigest, reinstall)}
	}
	return Status{Location: location, Installed: true, Detail: fmt.Sprintf("up to date (skill v%d, schema v%d, content %s)", skillVersion, schemaVersion, c.contentDigest)}
}

// Section returns the provider-neutral always-on AGENTS.md guidance.
func (c *Catalog) Section() string { return c.agentsSection }
