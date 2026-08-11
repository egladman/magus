// Package agent owns the two provider-neutral halves of Magus's agent surface: the
// agent-skill artifact (this file - command packages supply embedded source files, and
// this package renders, installs and verifies the generated surface without knowing about
// a particular CLI host), and the guard verdict wire contract (guard.go, which lives here
// because `package main` cannot be imported, so a parity check outside cmd/magus would
// otherwise have to restate it).
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
	"text/template"
	"time"
)

// SkillVersion changes when the installed skill contract changes. It is part
// of the generated provenance and lets verification explain stale installs.
const SkillVersion = 32

const skillLicense = "GPL-3.0-or-later"

const anchorSkillRel = "magus-query/SKILL.md"

// LocalSkillName is reserved for a workspace's OWN rules, and magus must never
// ship a skill by that name.
//
// Nothing in the installer or the verifier knows this constant, and that is the
// design rather than an omission: install writes only the names in
// skillSources, and CheckStatuses grades only the anchor, so any name magus
// does not ship is already untouchable. The reservation exists so it stays that
// way - a future shipped skill called magus-local-development would, on the
// first --force after the upgrade, silently overwrite every early adopter's
// file.
// TestLocalSkillNameIsReserved is what makes the promise enforceable.
const LocalSkillName = "magus-local-development"

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
//
// A {{if .Full}} branch alone caps how short the simple permutation can get,
// because it can only SUBTRACT. Simple is "everything minus the full-only
// branches", so a passage BOTH permutations must express sits in the shared
// text at whatever length the full form needs, and the only way to shorten it
// further is to drop it entirely and lose the step. The {{else}} arm is the
// one construct that reaches it: full keeps the long wording, simple gets the
// short one.
//
// Measured 2026-07-31 across the ten shipped skills: simple came out 20.3%
// smaller than full, on 137 full-only branches against only 28 {{else}} arms.
// The most prose-heavy simple forms are magus-run (82.6% prose) and magus-vcs
// (91.0% prose). The headroom is in the wording of the shared text, not in the
// tables. An earlier version of this comment blamed the tables; that was wrong,
// and it pointed authors at the one part of the page they should not touch.
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

// Full and Simple let a skill body branch on the permutation with {{if .Full}}.
// They exist because text/template cannot reference a package constant, so the
// predicate has to hang off the value being rendered.
func (v Variant) Full() bool { return v == VariantFull }

func (v Variant) Simple() bool { return v == VariantSimple }

// Is reports whether v is the named variant, so a third permutation costs a
// constant and a String case rather than a new pair of markers.
func (v Variant) Is(name string) bool { return v.String() == name }

// VariantOf maps a --simple boolean to a Variant, so the CLI does not spell the
// conditional at every call site.
func VariantOf(simple bool) Variant {
	if simple {
		return VariantSimple
	}
	return VariantFull
}

// applyVariant renders body for v. The body is a text/template, so a permutation
// is an ordinary {{if}} branch and a malformed one is a parse or execute error
// rather than text that silently survives into an installed file.
//
// A skill body that needs to SHOW template syntax (magus-run documents the
// `-o template` flag, magus-buzz documents mustache) escapes it as a string
// constant: {{"{{.Field}}"}}. That applies inside fenced code blocks too - the
// template engine does not know what Markdown is.
func applyVariant(name, body string, v Variant) (string, error) {
	t, err := template.New(name).Parse(body)
	if err != nil {
		return "", fmt.Errorf("skill %q: %w", name, err)
	}
	var b strings.Builder
	if err := t.Execute(&b, v); err != nil {
		return "", fmt.Errorf("skill %q: %w", name, err)
	}
	return tidyBlankLines(b.String()), nil
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
	{name: "magus-adapt", description: "Adapt magus's installed agent surface to THIS workspace without breaking it. Use when repeated friction is not covered by a shipped skill, when tempted to edit an installed magus-* SKILL.md (they are stamped: `magus graph verify` reports the edit as drift and the next `magus agent install --force` erases it), and when deciding whether a workspace rule should graduate upstream as a pull request or an issue. Workspace-specific rules belong in a local magus-local-development skill, stamped with their evidence and a retire-when condition.", bodyPath: "skills/magus-adapt/SKILL.md"},
	{name: "magus-architecture", description: "Ground refactoring and structure proposals in the magus knowledge graph instead of intuition. Use when suggesting directory structure, package layout, or module boundaries, when deciding where new code belongs, when assessing the blast radius or risk of a refactor, or when asked where a magus workspace's coupling and churn concentrate.", bodyPath: "skills/magus-architecture/SKILL.md"},
	{name: "magus-buzz", description: "Write and run Buzz, the language magusfiles, spells, and `magus buzz` scripts are written in. Use when writing or debugging a magusfile target, a spell, or a .buzz file, and when a one-off script is needed in a magus workspace - Buzz is already installed with the whole magus host surface (fs, http, json, yaml, template, vcs, ...), so it needs no dependency install. Also use when Buzz syntax surprises you: namespace access is a backslash, object literals use `=`, and `magus buzz` runs upstream-strict (no top-level control flow, every argument after the first must be labeled).", bodyPath: "skills/magus-buzz/SKILL.md"},
	{name: "magus-buzz-review", description: "Review Buzz code - a magusfile, a spell, or a standalone .buzz script - across three lenses run in parallel: idiom/style, skeptic/correctness, and upstream-Buzz conformance. Use when asked to review, audit, or critique a .buzz file or change, or when a finding needs to say whether it holds anywhere Buzz runs (UPSTREAM), only under gopherbuzz (GOPHERBUZZ), or runs here but not upstream (PORTABILITY). Fans out the three lenses via the Agent tool and merges the results, the same shape go-review-ultra uses for Go. Does NOT cover magusfile/target/spell contracts - caching, ctx.needs, wards, charms; use magus-buzz for those.", bodyPath: "skills/magus-buzz-review/SKILL.md"},
	{name: "magus-changes", description: "Summarize what changed in a magus workspace, write it up, or answer a granular diff question. Use for \"what's been merged lately?\", \"catch me up since last week\", \"add this to the CHANGELOG\", and \"what exactly did this branch change?\" Covers three outputs: a short evidence-backed brief, a Keep a Changelog entry in the repo's existing shape, and per-question diff commands. Always answer through magus surfaces (graph diff, describe file, affected --impact/--explain) rather than reading a raw diff; do not infer features from commit subjects alone.", bodyPath: "skills/magus-changes/SKILL.md"},
	{name: "magus-context-audit", description: "Audit the instructions an agent was given - the repo instruction file, installed skills, handoff-journal entries, a routing index, hook-injected text, and any user-level instruction file - for statements that contradict each other or that no longer match what the tools do. Use after changing a guard rule, a denied command, or a documented workflow; before shipping a change to the agent surface; and when an agent has been behaving inconsistently or ignoring a rule. This is a lens over INSTRUCTIONS, not over code: it reports ranked findings for a human to act on and never edits anything itself.", bodyPath: "skills/magus-context-audit/SKILL.md"},
	{name: "magus-delegate-ultra", description: "Plan and execute potentially expensive multi-agent work in a magus workspace as an acceptance-criteria loop, using affected shard plans and knowledge-graph evidence to assign collision-resistant edit units, coordinate nested delegation, and choose cost-appropriate effort tiers. Use ONLY when the user names this skill, or asks in their own words for the work to be SPLIT ACROSS AGENTS - \"fan this out\", \"run these in parallel\", \"use several subagents\", \"spin up an agent per package\". Wanting the work faster, sooner, or more thorough is NOT that request: those are asks about the outcome, and this skill is a choice about the method, with a real cost. Never auto-trigger it on ordinary implementation.", bodyPath: "skills/magus-delegate-ultra/SKILL.md"},
	{name: "magus-docs", description: "Traverse magus's own documentation to answer a \"how does magus do X / what does Y mean / where is Z documented\" question, instead of guessing an answer or a URL. Use when you need authoritative magus behavior (a CLI flag, a spell op, a diagnostic code, a config key, a stdlib module) and the workspace graph cannot give it. Do NOT use for facts about THIS workspace (use magus-query) or to run work (use magus-run).", bodyPath: "skills/magus-docs/SKILL.md"},
	{name: "magus-memory", description: "Maintain a user-owned handoff journal through magus_memory or `magus memory`: named decisions, plans, and pointers that survive worktrees and sessions. It is not automatic agent memory; add an entry only when a later person needs to reopen the linked graph/query/output/doc evidence. Verify malformed, stale, and broken-linked entries before relying on them.", bodyPath: "skills/magus-memory/SKILL.md"},
	{name: "magus-query", description: "Query the magus knowledge graph to find and relate entities (projects, targets, spells, ops, charms, modules, diagnostics, docs). Use INSTEAD of Grep or Glob in a repo with magusfile.buzz whenever the question is what exists, what depends on what, where something is used, or how two entities relate - a graph answer is verified against declared sources, a grep hit is a guess.", bodyPath: "skills/magus-query/SKILL.md"},
	{name: "magus-run", description: "Run builds, tests, lints, and codegen through magus targets. Use BEFORE typing go test, go build, npm test, npx, eslint, prettier, pytest, tsc, cargo, or any other raw language tool in a repo with magusfile.buzz at the root - a target covers the work, and the raw tool bypasses the cache, the sandbox, and affected tracking. Also use when a magus target fails and you need its captured output, and for the final pre-commit gate (magus affected ci).", bodyPath: "skills/magus-run/SKILL.md"},
	{name: "magus-sdk", description: "Help a Go developer consume magus as a library (import \"github.com/egladman/magus\") instead of shelling out to the CLI, and audit whether the SDK actually serves them. Use when someone wants to call Open/Inspect/Run from their own Go program, embed magus's workspace model in another tool, or asks \"can I use magus without the binary\". Also use to audit the SDK surface itself - whether a type is exported, a concept is reachable without the CLI, and whether a package boundary is deliberate or accidental. Do NOT use for CLI usage (magus-run, magus-query) or for editing magus's own source (magus-architecture).", bodyPath: "skills/magus-sdk/SKILL.md"},
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
	// filepath.IsAbs/~ catches an absolute escape but not "../../outside": Join
	// with dir still resolves that to a path outside it. Clean the joined result
	// and confirm it is still under dir before writing anything.
	joined := filepath.Clean(filepath.Join(dir, dest))
	if rel, err := filepath.Rel(dir, joined); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("agent install: destination %q escapes the working tree", dest)
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
	// "re-run to update" would be a lie now: re-running install PRINTS this block,
	// it does not rewrite the file. The one line a puzzled reader looks at has to
	// name the step magus cannot do for them.
	return fmt.Sprintf("<!-- magus:skills:begin generated by: magus agent install; agent-skill-version: %d; knowledge-schema-version: %d; skill-content: %s; do not edit - magus never writes this file, so re-run `magus agent install` and replace this block to update -->", SkillVersion, c.schemaVersion, c.contentDigest)
}

const agentsSectionEnd = "<!-- magus:skills:end -->"

var agentsSectionRe = regexp.MustCompile(`(?s)<!-- magus:skills:begin .*?-->.*?<!-- magus:skills:end -->`)

// AgentsBlock returns the managed magus guidance wrapped in its begin/end
// markers, ready to paste into a repo's AGENTS.md.
//
// Magus deliberately has no counterpart that WRITES this into AGENTS.md, and
// the reason is the same one that makes an installer appending to your
// .bashrc a bad neighbour: the file belongs to the developer, the merge logic
// is never as careful as it looks, and re-runs leave cruft nobody wrote and
// nobody can audit. Instruct, do not mutate. Reading AGENTS.md back to grade
// the block's stamp (CheckStatuses) is a different thing and stays.
func (c *Catalog) AgentsBlock() string {
	return c.agentsSectionBegin() + "\n\n" + strings.TrimSpace(c.agentsSection) + "\n\n" + agentsSectionEnd + "\n"
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
			// The fix names a PRINTING command, because magus does not write this
			// file: the block is the developer's to paste over the stale one. The
			// string has been wrong before - it once named a flag that does not
			// parse - and a stale stamp whose one job is to hand you the command
			// that fixes it is worth checking against `magus agent -h`.
			out = append(out, c.gradeStamp("AGENTS.md", "magus agent sample (prints the current block; magus does not write this file, so replace the stale one between the markers yourself)", string(section)))
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

// VariantSize returns the total rendered size of every skill in v, stamp
// included, so a caller can state the context cost of an install without
// performing one.
func (c *Catalog) VariantSize(v Variant) (int64, error) {
	skills, err := c.EmbeddedSkills(v)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, s := range skills {
		total += int64(len(c.StampSkill(c.RenderSkill(s), v)))
	}
	return total, nil
}
