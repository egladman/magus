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
//
// 37: a --simple install also writes each skill's always-full <name>-full twin
// (see fullTwinSuffix), and both entries carry a cross-reference in their
// description. The content digest cannot catch this on its own - it hashes the
// SOURCE bodies, which did not change, while what an install writes did.
// 38: magus-commit-composition - restructuring an unpushed branch into reviewable
// commits from project ownership, declared outputs and blast radius.
// 39: `magus graph verify` is gone; the installed copies are graded by `magus
// doctor`'s agent skills check, which every skill that named the old verb now
// points at.
// 40: magus-delegate-multi-agent learns two delegation failure modes observed
// in the field: a worker's actual base can differ from the handed checkpoint
// (verify it, or materialize and re-record), and a delegation whose environment
// cannot execute magus gets ROOT-DEFERRED validation up front.
// 41: the vocabulary drops "unit" - a row of the delegation ledger is a
// DELEGATION, in the skill, the ledger table, and the tools it names.
// 42: the delegation runtime reaches the skills - magus-delegate-multi-agent
// teaches ledger register, MAGUS_DELEGATION enrollment and the guard's
// deny/advise split, attention events for a blocked worker, and `magus sessions`
// as the audit of what a delegation ran; both it and magus-vcs-hygiene read
// `magus diff --cost` before landing.
const SkillVersion = 42

const skillLicense = "GPL-3.0-or-later"

const anchorSkillRel = "magus-query/SKILL.md"

// AgentsFile is the repo-root instruction file magus prints a managed block for but never
// writes, and the [Status.Location] CheckStatuses reports that block under - so a caller
// can tell the one location whose remedy magus cannot run.
const AgentsFile = "AGENTS.md"

// LocalSkillName is reserved for a workspace's OWN rules, and magus must never
// ship a skill by that name.
//
// Nothing in the installer or the verifier knows this constant, and that is the
// design rather than an omission: install writes only the names in skillSources,
// and grading skips any file magus did not write, so a name magus does not ship
// is untouchable by structure rather than by exception. The reservation exists so
// it stays that way - a future shipped skill called magus-local-development would,
// on the first --force after the upgrade, silently overwrite every early adopter's
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
	// Variant is what THIS entry was actually rendered as, independent of the
	// Variant requested from RenderedSkills. A simple request also returns
	// each skill's always-full twin (see fullTwinSuffix), and the twin's own
	// stamp must say "full", never "simple" - StampSkill and friends key off
	// this field, not the request. Meaningless on an unrendered definition
	// from EmbeddedSkills.
	Variant Variant
}

// Variant selects which permutation of a skill body to render.
//
// BOTH PERMUTATIONS ARE CURATED, and that is the whole design. The simple one is
// not a summary, a truncation, or a model-generated paraphrase: there is exactly
// one human-written body per skill, and its author brackets the spans that only
// the full permutation keeps. So the two can never come to describe different
// behavior, they share one content digest, and they version together - which is
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
// The most prose-heavy simple forms are magus-run (82.6% prose) and magus-vcs-hygiene
// (91.0% prose). The headroom is in the wording of the shared text, not in the
// tables. An earlier version of this comment blamed the tables; that was wrong,
// and it pointed authors at the one part of the page they should not touch.
type Variant int

const (
	// VariantFull is the default: every mechanical step spelled out, plus the
	// rationale that says why each one is the right move and what goes wrong
	// otherwise.
	VariantFull Variant = iota
	// VariantSimple sheds ENUMERATION and keeps JUDGMENT, for the most capable
	// readers - not the least. A capable reader can re-derive the mechanical
	// steps from the tool surface on its own; what it cannot re-derive is
	// which failures are silent, what is load-bearing, and where a judgment
	// call is being asked of it. So simple is a bet ON the reader, not a
	// lossy compression - which is why the split is a judgment an author
	// records, and why anything a step cannot survive losing belongs in the
	// unmarked core instead.
	VariantSimple
)

// fullTwinSuffix names the always-full twin a VariantSimple install writes
// alongside each skill's primary entry: <name>-full. Simple is a bet that the
// INSTALLING reader can re-derive what it drops - but a session that installs
// simple can still delegate to a smaller or less-briefed reader who cannot,
// and that reader inherits whatever the top-level install picked with no say
// in it. The twin gives it a stable name to ask for instead, independent of
// what tier the primary install happened to choose.
const fullTwinSuffix = "-full"

// FullTwinName returns the always-full twin's name for a base skill name.
func FullTwinName(base string) string { return base + fullTwinSuffix }

// IsFullTwinName reports whether name is a full twin rather than a primary
// skill entry. Callers that enumerate an INSTALLED tree need this: a simple
// install writes both, so a name-by-name comparison against the canonical
// skill list sees twins it would otherwise call unrecognized.
func IsFullTwinName(name string) bool { return strings.HasSuffix(name, fullTwinSuffix) }

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

// applyVariant renders body for v. The body is a text/template, so a permutation
// is an ordinary {{if}} branch and a malformed one is a parse or execute error
// rather than text that silently survives into an installed file.
//
// A skill body that needs to SHOW template syntax (magus-run documents the
// `-o template` flag, magus-buzz-write documents mustache) escapes it as a string
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
	// compat(until: the published docs no longer serve a page per skill under a
	// stable URL - observe by checking whether reference/skills/<name>/ still
	// appears in the released llms.txt): names this skill shipped under before.
	//
	// A rename breaks two different things, and this fixes only one of them. An
	// installed skill's DIRECTORY is pruned and rewritten, so a host picks the new
	// name up on the next install; a published doc URL is a link someone else
	// wrote, and nothing on their end re-runs. So the old names live here rather
	// than in a hand-kept redirect list beside the site: the rename is a fact
	// about the skill, and the page that carries the redirect is generated from
	// exactly this row.
	formerNames []string
}

// FormerNames returns the names a skill previously shipped under, oldest first,
// or nil for one that has never been renamed.
func FormerNames(name string) []string {
	for _, s := range skillSources {
		if s.name == name {
			return s.formerNames
		}
	}
	return nil
}

var skillSources = []skillSource{
	{name: "magus-workspace-rules", description: "Adapt magus's installed agent surface to THIS workspace without breaking it. Use when repeated friction is not covered by a shipped skill, when tempted to edit an installed magus-* SKILL.md (they are stamped: `magus doctor` reports the edit as drift and the next `magus agent install --force` erases it), and when deciding whether a workspace rule should graduate upstream as a pull request or an issue. Workspace-specific rules belong in a local magus-local-development skill, stamped with their evidence and a retire-when condition.", bodyPath: "skills/magus-workspace-rules/SKILL.md", formerNames: []string{"magus-adapt"}},
	{name: "magus-architecture-review", description: "Ground refactoring and structure proposals in the magus knowledge graph instead of intuition. Use when suggesting directory structure, package layout, or module boundaries, when deciding where new code belongs, when assessing the blast radius or risk of a refactor, or when asked where a magus workspace's coupling and churn concentrate.", bodyPath: "skills/magus-architecture-review/SKILL.md", formerNames: []string{"magus-architecture"}},
	{name: "magus-buzz-write", description: "Write and run Buzz, the language magusfiles, spells, and `magus buzz` scripts are written in. Use when writing or debugging a magusfile target, a spell, or a .buzz file, and when a one-off script is needed in a magus workspace - Buzz is already installed with the whole magus host surface (fs, http, json, yaml, template, vcs, ...), so it needs no dependency install. Also use when Buzz syntax surprises you: namespace access is a backslash, object literals use `=`, and `magus buzz` runs upstream-strict (no top-level control flow, every argument after the first must be labeled).", bodyPath: "skills/magus-buzz-write/SKILL.md", formerNames: []string{"magus-buzz"}},
	{name: "magus-buzz-review", description: "Review Buzz code - a magusfile, a spell, or a standalone .buzz script - across three lenses run in parallel: idiom/style, skeptic/correctness, and upstream-Buzz conformance. Use when asked to review, audit, or critique a .buzz file or change, or when a finding needs to say whether it holds anywhere Buzz runs (UPSTREAM), only under gopherbuzz (GOPHERBUZZ), or runs here but not upstream (PORTABILITY). Fans out the three lenses via the Agent tool and merges the results, the same shape go-review-ultra uses for Go. Does NOT cover magusfile/target/spell contracts - caching, ctx.needs, wards, charms; use magus-buzz-write for those.", bodyPath: "skills/magus-buzz-review/SKILL.md"},
	{name: "magus-change-summary", description: "Summarize what changed in a magus workspace, write it up, or answer a granular diff question. Use for \"what's been merged lately?\", \"catch me up since last week\", \"add this to the CHANGELOG\", and \"what exactly did this branch change?\" Covers three outputs: a short evidence-backed brief, a Keep a Changelog entry in the repo's existing shape, and per-question diff commands. Always answer through magus surfaces (graph diff, describe file, affected --impact/--explain) rather than reading a raw diff; do not infer features from commit subjects alone.", bodyPath: "skills/magus-change-summary/SKILL.md", formerNames: []string{"magus-changes"}},
	{name: "magus-commit-composition", description: "Restructure an UNPUSHED branch so each commit is one reviewable idea, using the workspace's own boundaries (project ownership, declared outputs, blast radius) rather than guessing from paths. Use when a branch has accumulated commits in the order the work occurred, before opening a PR, when asked to reconsolidate/squash/reword/clean up commits, or when a reviewer would meet a rename split across commits and a fix buried in a regeneration. Do NOT use on pushed commits, and do NOT use it to write a single message - that is idiomatic-commit-messages; this decides what goes IN each commit.", bodyPath: "skills/magus-commit-composition/SKILL.md"},
	{name: "magus-context-audit", description: "Audit the instructions an agent was given - the repo instruction file, installed skills, handoff-journal entries, a routing index, hook-injected text, and any user-level instruction file - for statements that contradict each other or that no longer match what the tools do. Use after changing a guard rule, a denied command, or a documented workflow; before shipping a change to the agent surface; and when an agent has been behaving inconsistently or ignoring a rule. This is a lens over INSTRUCTIONS, not over code: it reports ranked findings for a human to act on and never edits anything itself.", bodyPath: "skills/magus-context-audit/SKILL.md"},
	{name: "magus-delegate-multi-agent", description: "Split work across agents in a magus workspace as an acceptance-criteria loop: partition by WRITE SET using graph evidence (magus refs --occurrences, explain, affected --plan --stdin), prove the delegations cannot collide, bound fan-out depth, and match each delegation's model to the work it needs. Use when a change needs several disjoint groups of files edited, when an audit or review covers a tree, or when the user says \"fan this out\" or \"spin up an agent per package\" - you do not need to be asked. Do NOT fan out one coherent edit just because it invalidates many projects: a shard plan partitions VALIDATION, not editing, so it can veto a fan-out but never license one.", bodyPath: "skills/magus-delegate-multi-agent/SKILL.md", formerNames: []string{"magus-delegate-ultra"}},
	{name: "magus-docs-lookup", description: "Traverse magus's own documentation to answer a \"how does magus do X / what does Y mean / where is Z documented\" question, instead of guessing an answer or a URL. Use when you need authoritative magus behavior (a CLI flag, a spell op, a diagnostic code, a config key, a stdlib module) and the workspace graph cannot give it. Do NOT use for facts about THIS workspace (use magus-query) or to run work (use magus-run).", bodyPath: "skills/magus-docs-lookup/SKILL.md", formerNames: []string{"magus-docs"}},
	{name: "magus-handoff-journal", description: "Maintain a user-owned handoff journal through magus_memory or `magus memory`: named decisions, plans, and pointers that survive worktrees and sessions. It is not automatic agent memory; add an entry only when a later person needs to reopen the linked graph/query/output/doc evidence. Verify malformed, stale, and broken-linked entries before relying on them.", bodyPath: "skills/magus-handoff-journal/SKILL.md", formerNames: []string{"magus-memory"}},
	{name: "magus-query", description: "Query the magus knowledge graph to find and relate entities (projects, targets, spells, ops, charms, modules, diagnostics, docs). Use INSTEAD of Grep or Glob in a repo with magusfile.buzz whenever the question is what exists, what depends on what, where something is used, or how two entities relate - a graph answer is verified against declared sources, a grep hit is a guess.", bodyPath: "skills/magus-query/SKILL.md"},
	{name: "magus-run", description: "Run builds, tests, lints, and codegen through magus targets. Use BEFORE typing go test, go build, npm test, npx, eslint, prettier, pytest, tsc, cargo, or any other raw language tool in a repo with magusfile.buzz at the root - a target covers the work, and the raw tool bypasses the cache, the sandbox, and affected tracking. Also use when a magus target fails and you need its captured output, and for the final pre-commit gate (magus affected ci).", bodyPath: "skills/magus-run/SKILL.md"},
	{name: "magus-sdk", description: "Help a Go developer consume magus as a library (import \"github.com/egladman/magus\") instead of shelling out to the CLI, and audit whether the SDK actually serves them. Use when someone wants to call Open/Inspect/Run from their own Go program, embed magus's workspace model in another tool, or asks \"can I use magus without the binary\". Also use to audit the SDK surface itself - whether a type is exported, a concept is reachable without the CLI, and whether a package boundary is deliberate or accidental. Do NOT use for CLI usage (magus-run, magus-query) or for editing magus's own source (magus-architecture-review).", bodyPath: "skills/magus-sdk/SKILL.md"},
	{name: "magus-vcs-hygiene", description: "Safe git operations in a magus workspace (any repo with magusfile.buzz at the root). Use IMMEDIATELY before git commit, git add, git stash, git reset, git checkout, or git clean, and when reading git status or a diff - especially one touching MAGUS.md, gen/ trees, lockfiles, or other generated files. Classifies every changed path as generated output vs source (magus describe file), gives the commit checklist, and settles merge conflicts in generated files by regenerating. Do NOT stash or reset the whole tree to verify a build; load this skill first.", bodyPath: "skills/magus-vcs-hygiene/SKILL.md", formerNames: []string{"magus-vcs"}},
}

// Catalog binds the skill source assets embedded by the application to the
// schema version of that application. It contains no CLI parsing or rendering.
type Catalog struct {
	sourceFS      fs.FS
	agentsSection string
	schemaVersion int
	contentDigest string
	skillDigests  map[string]string
}

func NewCatalog(sourceFS fs.FS, agentsSection string, schemaVersion int) *Catalog {
	c := &Catalog{sourceFS: sourceFS, agentsSection: agentsSection, schemaVersion: schemaVersion}
	c.contentDigest = c.computeContentDigest()
	c.skillDigests = c.computeSkillDigests()
	return c
}

// SkillDigest fingerprints ONE skill: its body, name, and description.
//
// A catalog-wide digest restamped all 26 installed files and all 16 reference
// pages whenever any skill changed, so a diff could not show which one moved.
// Both permutations of a skill still share this value - see StampSkill. The
// catalog-wide contentDigest survives for the AGENTS.md block, which routes to
// every skill by name and so does depend on the whole set.
func (c *Catalog) SkillDigest(name string) string {
	// A twin is rendered from its primary's body, so it resolves to the primary's
	// entry rather than one of its own - that is what makes the two report the
	// same digest and go stale together.
	if d, ok := c.skillDigests[baseSkillName(name)]; ok {
		return d
	}
	return "unreadable"
}

func (c *Catalog) computeSkillDigests() map[string]string {
	out := make(map[string]string, len(skillSources))
	for _, source := range skillSources {
		body, err := fs.ReadFile(c.sourceFS, source.bodyPath)
		if err != nil {
			out[source.name] = "unreadable"
			continue
		}
		h := sha256.New()
		// The name and description are stamped alongside the body and are what a
		// host lists a skill by, so a description edit has to move the digest too.
		if _, err := fmt.Fprintf(h, "%s\n%s\n", source.name, source.description); err != nil {
			out[source.name] = "unreadable"
			continue
		}
		if _, err := h.Write(body); err != nil {
			out[source.name] = "unreadable"
			continue
		}
		out[source.name] = hex.EncodeToString(h.Sum(nil))[:12]
	}
	return out
}

// EmbeddedSkills returns every embedded skill's canonical, unrendered
// definition, in name order: Body carries the raw template source, exactly as
// checked in. Variant is meaningless on these entries - render one for a
// specific permutation with Render, or get the full install-ready list
// (twins included) with RenderedSkills.
func (c *Catalog) EmbeddedSkills() ([]AgentSkill, error) {
	sources := append([]skillSource(nil), skillSources...)
	sort.Slice(sources, func(i, j int) bool { return sources[i].name < sources[j].name })
	skills := make([]AgentSkill, 0, len(sources))
	for _, source := range sources {
		body, err := fs.ReadFile(c.sourceFS, source.bodyPath)
		if err != nil {
			return nil, err
		}
		skills = append(skills, AgentSkill{Name: source.name, Description: source.description, Body: strings.TrimSpace(string(body))})
	}
	return skills, nil
}

// Render renders def's raw template Body for v, returning a new AgentSkill
// whose Body is the final Markdown and whose Variant records which
// permutation produced it - RenderSkill and StampSkill key off that field on
// the RESULT, never off an ambient caller-supplied variant, so a mixed batch
// (see RenderedSkills) stamps every entry correctly regardless of what was
// requested. def is not mutated.
func (c *Catalog) Render(def AgentSkill, v Variant) (AgentSkill, error) {
	rendered, err := applyVariant(def.Name, def.Body, v)
	if err != nil {
		return AgentSkill{}, err
	}
	return AgentSkill{Name: def.Name, Description: def.Description, Body: rendered, Variant: v}, nil
}

// RenderedSkills returns every embedded skill rendered for v, in name order -
// the install-ready list SkillBytes, SkillTar, and WriteSkillTree all write.
// When v is VariantSimple, each skill is followed immediately by its
// always-full <name>-full twin (see fullTwinSuffix), so every one of those
// callers gets the dual install for free. VariantFull adds no twins: the
// primary entry already IS the full form, so a twin would only duplicate it
// under a second name.
func (c *Catalog) RenderedSkills(v Variant) ([]AgentSkill, error) {
	defs, err := c.EmbeddedSkills()
	if err != nil {
		return nil, err
	}
	skills := make([]AgentSkill, 0, len(defs))
	for _, def := range defs {
		primary, err := c.Render(def, v)
		if err != nil {
			return nil, err
		}
		// Deliberately NOT cross-referenced from the primary's description. The
		// twin's own description already announces itself in the host's skill
		// listing, where a delegated model browsing for a skill sees it; adding a
		// pointer here would spend context on every simple skill to say something
		// the twin's own entry already says, and simple exists to spend less.
		skills = append(skills, primary)

		if v != VariantSimple {
			continue
		}
		full, err := c.Render(def, VariantFull)
		if err != nil {
			return nil, err
		}
		full.Name = FullTwinName(def.Name)
		full.Description = def.Description + " This is the full reference copy of " + def.Name +
			" - prefer it over " + def.Name + " if you are a smaller or delegated model."
		skills = append(skills, full)
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
	skills, err := c.RenderedSkills(v)
	if err != nil {
		return nil, err
	}
	for _, skill := range skills {
		if skill.Name == name {
			return c.StampSkill(skill.Name, c.RenderSkill(skill), skill.Variant), nil
		}
	}
	return nil, fmt.Errorf("unknown skill %q", name)
}

// SkillNames returns the embedded skill names in deterministic order.
func (c *Catalog) SkillNames() ([]string, error) {
	skills, err := c.EmbeddedSkills()
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
	skills, err := c.RenderedSkills(v)
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
		body := c.StampSkill(skill.Name, c.RenderSkill(skill), skill.Variant)
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

// PlanSkillTree returns the paths WriteSkillTree would write, writing nothing.
//
// Shares checkDestination with the writer, so a plan cannot name paths the run
// would not. The --force conflict check is not repeated: a plan reports what a
// successful run produces.
func (c *Catalog) PlanSkillTree(dir, dest string, v Variant) ([]string, error) {
	if err := checkDestination(dir, dest); err != nil {
		return nil, err
	}
	skills, err := c.RenderedSkills(v)
	if err != nil {
		return nil, err
	}
	planned := make([]string, 0, len(skills))
	for _, skill := range skills {
		planned = append(planned, filepath.Join(dest, skill.Name, "SKILL.md"))
	}
	return planned, nil
}

// checkDestination refuses a destination that lands outside dir. The joined path
// is cleaned and re-checked because "../../outside" is neither absolute nor ~.
func checkDestination(dir, dest string) error {
	if filepath.IsAbs(dest) || strings.HasPrefix(dest, "~") {
		return fmt.Errorf("agent install: destination %q is outside the working tree; pass --global or use --tar | tar -xf - -C <dir>", dest)
	}
	joined := filepath.Clean(filepath.Join(dir, dest))
	if rel, err := filepath.Rel(dir, joined); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("agent install: destination %q escapes the working tree", dest)
	}
	return nil
}

// WriteSkillTree renders the standard Agent Skills format into <dir>/<dest>.
// The destination must be a path relative to <dir>; absolute paths are
// refused so magus never silently writes outside the working tree. The
// caller is responsible for that guard at the CLI surface; this method
// enforces it for safety.
func (c *Catalog) WriteSkillTree(dir, dest string, force bool, v Variant) ([]string, error) {
	if err := checkDestination(dir, dest); err != nil {
		return nil, err
	}
	skills, err := c.RenderedSkills(v)
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
		if err := os.WriteFile(outPath, c.StampSkill(skill.Name, c.RenderSkill(skill), skill.Variant), 0o644); err != nil {
			return nil, fmt.Errorf("agent install: write %s: %w", outPath, err)
		}
		written = append(written, filepath.Join(dest, rel))
	}
	return written, nil
}

// StaleSkillDirs returns the installed skill directories under <dir>/<dest> that
// magus wrote and this binary no longer ships, as <dest>-relative paths.
//
// Detection is separate from removal, and both halves matter. A generator that owns
// its writes but not its deletions leaves a mess that outlives every reason for it:
// renaming a skill leaves the old directory installed, still stamped, still loaded
// by the host, still teaching whatever it said the day it was orphaned. Nothing
// reported it either - a drift gate compares the files a generator DECLARES against
// what it wrote, and an extra file is in neither set. This is what makes it
// reportable; PruneSkillTree is what acts on it, and only when asked.
//
// The stamp is the authority on what is a candidate at all, and that is the whole
// safety story: magus considers only files it can prove it wrote. A directory with
// no SKILL.md, or a SKILL.md without the generated footer, is someone's
// hand-authored skill sitting in the same folder (magus-skill-authoring and a
// workspace's own magus-local-development both live there) and is never a candidate
// even though the catalog does not name it.
func (c *Catalog) StaleSkillDirs(dir, dest string) ([]string, error) {
	shipped, err := c.shipped()
	if err != nil {
		return nil, err
	}

	root := filepath.Join(dir, dest)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("agent install: read %s: %w", root, err)
	}
	var stale []string
	for _, e := range entries {
		if !e.IsDir() || shipped[e.Name()] {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, e.Name(), "SKILL.md"))
		if err != nil || !bytes.Contains(body, []byte(generatedSkillMarker)) {
			continue // not ours to delete
		}
		stale = append(stale, filepath.Join(dest, e.Name()))
	}
	return stale, nil
}

// PruneSkillTree removes the stale skill directories under <dir>/<dest> and returns
// what it removed.
//
// Never a side effect of installing. Install writes files it can name in advance;
// this deletes files the caller has not seen, chosen by a rule that lives in a
// binary they may have just upgraded. Those are different enough acts that the
// second one asks - so install reports what is stale and names this, and a person
// decides. The stamp makes the deletion safe; it does not make it expected.
func (c *Catalog) PruneSkillTree(dir, dest string) ([]string, error) {
	stale, err := c.StaleSkillDirs(dir, dest)
	if err != nil {
		return nil, err
	}
	for _, rel := range stale {
		if err := os.RemoveAll(filepath.Join(dir, rel)); err != nil {
			return nil, fmt.Errorf("agent install: remove %s: %w", rel, err)
		}
	}
	return stale, nil
}

// generatedSkillMarker is the substring of the footer that proves magus wrote a
// SKILL.md. Deliberately the version-free prefix: a file this binary is about to
// delete was written by SOME magus, usually an older one, so matching the current
// version would make pruning fail exactly when it is needed.
const generatedSkillMarker = "generated by: magus agent install"

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
// .bashrc a bad neighbor: the file belongs to the developer, the merge logic
// is never as careful as it looks, and re-runs leave cruft nobody wrote and
// nobody can audit. Instruct, do not mutate. Reading AGENTS.md back to grade
// the block's stamp (CheckStatuses) is a different thing and stays.
func (c *Catalog) AgentsBlock() string {
	return c.agentsSectionBegin() + "\n\n" + strings.TrimSpace(c.agentsSection) + "\n\n" + agentsSectionEnd + "\n"
}

func (c *Catalog) provenance(name string, v Variant) string {
	return fmt.Sprintf("license: %s\ncompatibility: any-agent\nmetadata:\n  source: magus\n  agent-skill-version: %d\n  knowledge-schema-version: %d\n  skill-content: %s\n  skill-variant: %s\n", skillLicense, SkillVersion, c.schemaVersion, c.SkillDigest(name), v)
}

func (c *Catalog) footer(name string, v Variant) string {
	return fmt.Sprintf("\n<!-- generated by: magus agent install; agent-skill-version: %d; knowledge-schema-version: %d; skill-content: %s; skill-variant: %s; do not edit, re-run to update -->\n", SkillVersion, c.schemaVersion, c.SkillDigest(name), v)
}

// StampSkill injects provenance frontmatter and appends a generated-by footer.
//
// The stamp names the variant but keeps the SOURCE content digest, deliberately:
// both permutations come from one body, so they must report the same digest and go
// stale together. A per-variant digest would let a simple install look current
// against a source its full sibling had already outgrown.
func (c *Catalog) StampSkill(name string, body []byte, v Variant) []byte {
	body = c.injectProvenance(name, body, v)
	return append([]byte(strings.TrimRight(string(body), "\n")+"\n"), c.footer(name, v)...)
}

func (c *Catalog) injectProvenance(name string, body []byte, v Variant) []byte {
	s := string(body)
	if !strings.HasPrefix(s, "---\n") {
		return body
	}
	rel := strings.Index(s[len("---\n"):], "\n---")
	if rel < 0 {
		return body
	}
	closeAt := len("---\n") + rel + 1
	return []byte(s[:closeAt] + c.provenance(name, v) + s[closeAt:])
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
		// The anchor decides only whether magus is installed HERE; grading is
		// per-skill below, so its contents are not read.
		path := filepath.Join(dir, dest, anchorSkillRel)
		_, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			out = append(out, Status{Location: dest, Installed: true, Stale: true, Detail: "cannot read installed skill: " + err.Error()})
			continue
		}
		out = append(out, c.gradeDest(dir, dest))
	}
	if body, err := os.ReadFile(filepath.Join(dir, AgentsFile)); err == nil {
		if section := agentsSectionRe.Find(body); section != nil {
			// The fix names a PRINTING command, because magus does not write this
			// file: the block is the developer's to paste over the stale one. The
			// string has been wrong before - it once named a flag that does not
			// parse - and a stale stamp whose one job is to hand you the command
			// that fixes it is worth checking against `magus agent -h`.
			out = append(out, c.gradeStamp(AgentsFile, "magus agent sample (prints the current block; magus does not write this file, so replace the stale one between the markers yourself)", string(section), c.contentDigest))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Location < out[j].Location })
	return out
}

// gradeDest grades every magus skill installed under dest, not just the anchor.
//
// Reading one skill worked only while the digest covered the whole catalog. With
// a per-skill digest that shortcut goes blind: a stale magus-run reads as current
// when magus-query happens not to have changed.
func (c *Catalog) gradeDest(dir, dest string) Status {
	reinstall := "magus agent install " + dest + " --force"
	// An unusable shipped set grades EVERYTHING rather than skipping: the skip below
	// reads an unknown name as "not magus's", which would silently drop a
	// pre-versioning install of a shipped skill - the one case that must still report.
	shipped, shippedErr := c.shipped()
	for _, name := range c.installedSkillNames(filepath.Join(dir, dest)) {
		body, err := os.ReadFile(filepath.Join(dir, dest, name, "SKILL.md"))
		if err != nil {
			continue
		}
		// Not ours to grade: a workspace's own skill sits here by design, and grading it
		// reported drift no reinstall could clear, since install writes only the names
		// magus ships.
		//
		// BOTH conditions, and neither alone is the test. A missing marker also
		// describes an install PREDATING versioning, which must still read as stale; an
		// unshipped name also describes a skill a rename orphaned, which magus wrote.
		if shippedErr == nil && !shipped[name] && !bytes.Contains(body, []byte(generatedSkillMarker)) {
			continue
		}
		if st := c.gradeStamp(dest, reinstall, string(body), c.SkillDigest(baseSkillName(name))); st.Stale {
			return Status{Location: dest, Installed: true, Stale: true, Detail: name + ": " + st.Detail}
		}
	}
	return Status{Location: dest, Installed: true, Detail: fmt.Sprintf("up to date (skill v%d, schema v%d)", SkillVersion, c.schemaVersion)}
}

// installedSkillNames lists the magus skill directories under path, sorted. One
// magus no longer ships is included: the host still loads it.
func (c *Catalog) installedSkillNames(path string) []string {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "magus-") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// shipped is the set of directory names an install of this catalog writes. Both
// variants are included, so a tree installed under either one is judged by what the
// catalog SHIPS rather than by which permutation last wrote it.
//
// The error is propagated rather than absorbed into an empty set: callers read this
// as "magus did not write that", and an empty set says it of every skill magus owns -
// which would make StaleSkillDirs offer the whole installed tree for deletion.
func (c *Catalog) shipped() (map[string]bool, error) {
	skills, err := c.RenderedSkills(VariantSimple)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(skills)*2)
	for _, s := range skills {
		out[s.Name] = true
		out[FullTwinName(s.Name)] = true
	}
	return out, nil
}

// baseSkillName maps an installed directory to the skill it was rendered from,
// so a `-full` twin grades against its primary's digest rather than missing.
func baseSkillName(dir string) string {
	return strings.TrimSuffix(dir, fullTwinSuffix)
}

func (c *Catalog) gradeStamp(location, reinstall, body, wantDigest string) Status {
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
	if d[1] != wantDigest {
		return Status{Location: location, Installed: true, Stale: true, Detail: fmt.Sprintf("content differs from this binary's embedded skills (installed %s, binary %s); re-run: %s", d[1], wantDigest, reinstall)}
	}
	return Status{Location: location, Installed: true, Detail: fmt.Sprintf("up to date (skill v%d, schema v%d, content %s)", skillVersion, schemaVersion, wantDigest)}
}

// Section returns the provider-neutral always-on AGENTS.md guidance.
func (c *Catalog) Section() string { return c.agentsSection }

// VariantSize returns the total rendered size of every skill's PRIMARY entry
// in v, stamp included, so a caller can state the context cost of an install
// without performing one. Deliberately excludes RenderedSkills' full twins -
// reportContextCost uses this to compare "what you have" against "what the
// other variant would be", and a twin-inclusive total would make VariantSize
// (VariantSimple) larger than VariantSize(VariantFull) precisely because
// simple installs more files, silently inverting the comparison it exists to
// answer ("would --simple cost less").
func (c *Catalog) VariantSize(v Variant) (int64, error) {
	defs, err := c.EmbeddedSkills()
	if err != nil {
		return 0, err
	}
	var total int64
	for _, def := range defs {
		rendered, err := c.Render(def, v)
		if err != nil {
			return 0, err
		}
		total += int64(len(c.StampSkill(rendered.Name, c.RenderSkill(rendered), v)))
	}
	return total, nil
}
