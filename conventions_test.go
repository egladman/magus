package magus

// Source-level convention guards: assertions about the SHAPE of this repository
// rather than the behavior of any symbol in it. They scan the tree as text because
// what they prevent has no runtime signal to observe.
//
// None of them pairs with a source file, and that is the point rather than an
// oversight - their subject is an agreement across artifacts, not a Go symbol. They
// live together so the tree carries one file named for that subject instead of four
// named after nothing, which is how hookdocs_test.go once advertised a hookdocs.go
// that never existed.

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/describe"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A source-level check over the Buzz this repository ships, in the spirit of
// cmd/magus's TestEveryCommandBindsDisplayFlags: a text scan, because the thing
// it prevents cannot be observed at runtime without rendering the whole site and
// watching memory.

// scanLoopRe matches `while (<expr>.indexOf(...) != null)`, the shape of a loop
// that rescans a string it is also rewriting.
var scanLoopRe = regexp.MustCompile(`while\s*\([^)]*\.indexOf\([^)]*\)\s*!=\s*null`)

// buzzScanOptOut lets a genuine case through, and demands a reason in the same
// breath - the same shape as the repo's other acknowledged suppressions.
const buzzScanOptOut = "buzz-scan-ok:"

// TestNoRescanningStringLoops keeps a quadratic, memory-retaining idiom out of
// the Buzz sources.
//
// `while (s.indexOf(x) != null) { s = s.replace(x, y) }` is the natural way to
// write replace-all in Buzz, because str.replace substitutes only the FIRST
// occurrence. It is also the most expensive way: each pass copies the whole
// string, and on the default VM build every copy is a distinct string interned
// for the life of the process and never freed (see libs/gopherbuzz/vm/value.go -
// the intern table has no eviction, and it is what bounds the never-freed heap,
// so the fix cannot be eviction). Removing three of these from the docs render
// cut its measured peak from 5806MB to 4259MB.
//
// The replacements:
//
//	s.split(x).join(y)                 replace-all, one pass
//	s.split(x).len() - 1               count occurrences, one pass
//
// Neither is a drop-in for collapsing RUNS of a substring: splitting on two
// spaces leaves the odd one behind. Split on one and drop the empty parts.
func TestNoRescanningStringLoops(t *testing.T) {
	var findings []string

	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// An unreadable subtree is not this gate's business: it scans the
			// sources that ARE readable and says nothing about the rest, rather
			// than failing a lint over a permissions quirk.
			return nil //nolint:nilerr // deliberate: skip, do not abort the walk
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "worktrees", "gen", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".buzz" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil //nolint:nilerr // same: an unreadable file is skipped, not fatal
		}
		lines := strings.Split(string(body), "\n")
		for i, line := range lines {
			if !scanLoopRe.MatchString(line) {
				continue
			}
			// An opt-out on the loop or the line above it.
			if strings.Contains(line, buzzScanOptOut) ||
				(i > 0 && strings.Contains(lines[i-1], buzzScanOptOut)) {
				continue
			}
			findings = append(findings, path+":"+itoa(i+1)+": "+strings.TrimSpace(line))
		}
		return nil
	})
	require.NoError(t, err)

	assert.Emptyf(t, findings,
		"a string loop that rescans what it rewrites copies the whole string per pass,\n"+
			"and every copy is interned for the life of the process:\n  %s\n\n"+
			"Use s.split(x).join(y) to replace all, or s.split(x).len()-1 to count.\n"+
			"To collapse RUNS, split on ONE separator and drop the empty parts.\n"+
			"If a loop genuinely has to rescan, put `%s <reason>` on it.",
		strings.Join(findings, "\n  "), buzzScanOptOut)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// bearerRe matches a `token=` carrying something long enough to be a real bearer
// rather than a placeholder. auth.Generate mints 256 bits base64url-encoded, so a
// live token is 43 characters; the documented forms (`token=...`, `token=<bearer>`)
// are far shorter and contain characters this class excludes.
var bearerRe = regexp.MustCompile(`token=[A-Za-z0-9_-]{20,}`)

// TestDocsCarryNoBearerToken keeps a live daemon token out of committed documentation.
//
// docs/concepts/knowledge.md is generated by cmd/magus-examples, which captures the
// real stdout of `magus explain`. That output ends with a Graph Explorer deep-link,
// and the link carries the daemon auth token - auth.Load reads it from a file in the
// state dir whether or not a daemon is running. So the page committed a real token
// from whichever machine last regenerated it, and the docs site published it.
//
// It also made the page machine-dependent, which is how it was found: a runner has no
// token file, regenerated the link without one, and the drift gate failed on CI while
// passing on every developer machine. The capture now runs with XDG_STATE_HOME pointed
// into its own fixture, so there is no token to find.
//
// Documented placeholders stay legal - docs/reference/console.md explains the scheme
// with `#token=<bearer>`, which is the useful thing to write.
func TestDocsCarryNoBearerToken(t *testing.T) {
	var findings []string

	err := filepath.WalkDir("docs", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable subtree is skipped, not fatal
		}
		if d.IsDir() {
			// gen/ is rendered output, not committed (see the publish workflow).
			if d.Name() == "gen" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil //nolint:nilerr // same
		}
		for i, line := range strings.Split(string(body), "\n") {
			if m := bearerRe.FindString(line); m != "" {
				findings = append(findings, path+":"+itoa(i+1)+": "+m)
			}
		}
		return nil
	})
	require.NoError(t, err)

	assert.Emptyf(t, findings,
		"committed documentation carries what looks like a live bearer token:\n  %s\n\n"+
			"Regenerate the page rather than editing it, and rotate the token - a doc site is\n"+
			"public. Write a placeholder (`token=<bearer>`) when documenting the scheme.",
		strings.Join(findings, "\n  "))
}

// Dogfooding checks: assertions that this repository actually uses what it publishes.
//
// This file deliberately has no dogfood.go beside it, and it is the one place in the tree
// where that is the point rather than an oversight. Its subject is not a Go symbol but the
// agreement between three artifacts - the repo's own config, the guide, and the templates a
// reader downloads - so there is nothing for it to pair with, and naming it after any one of
// them (it was hookdocs_test.go) advertised a hookdocs.go that never existed. Anything else
// asserting "we use what we ship" belongs here too.
//
// The guard hook this repository dogfoods, the one its documentation teaches,
// and the one a reader downloads must all be the SAME file. They drifted once
// already: the docs kept advertising `command -v magus || exit 0` after the
// shipped config had moved on, so a reader copying the documented form got a
// guard that fails open in silence - the exact failure the change removed.
//
// The templates under docs/guides/integrations/agents/ are the source of truth. This
// repository's own config invokes them rather than inlining a copy, so
// dogfooding exercises the artifact readers actually get. These tests keep that
// true: a config that stops referencing a template, or a template that stops
// being embedded in the guide, is a test failure rather than something only a
// careful reader would notice.

const (
	dogfoodedHookConfig = ".claude/settings.json"
	hookGuideDoc        = "docs/guides/integrations/agents.md"
	hookTemplateDir     = "docs/guides/integrations/agents"

	// rootMagusfile carries skills_generate's declared outputs, which decide both
	// what `magus describe file` calls generated and what EnsureMergeDriver writes
	// into .gitattributes.
	rootMagusfile = "magusfile.buzz"
	// embeddedSkillDir is the shipped set: one directory per skill magus installs.
	embeddedSkillDir = "internal/agent/skills"
)

// handAuthoredSkills live beside the installed ones and are NOT written by
// `magus agent install`. Both are tracked through explicit .gitignore negations,
// and magus-workspace-rules tells readers to put local rules in exactly this
// shape, so a declaration that claims them tells an author the one file they are
// supposed to edit is generated - and hands it to the regenerating merge driver
// on a conflict.
var handAuthoredSkills = []string{"magus-skill-authoring", "magus-local-development"}

// skillOutputGlob matches the declared-output patterns in skills_generate. The
// install calls in the same body name a bare destination directory with no
// trailing pattern, so they cannot match.
var skillOutputGlob = regexp.MustCompile(`"(\.(?:claude|agents|opencode)/skills/[^"]+)"`)

// TestSkillsGenerateDeclaresEveryShippedSkill keeps the declared outputs in step
// with what magus actually installs, in both directions.
//
// The declaration used to be `.claude/skills/**` and friends, which was one line
// per destination and wrong in a way nothing caught: it claimed the hand-authored
// skills too. Naming the shipped skills instead is correct but goes stale on its
// own, so this is the gate that makes adding a skill fail here rather than ship an
// installed file magus calls hand-editable.
// guardAdviceSkillCoverage maps each advisory the guard can emit to a token that
// must appear in some installed skill.
//
// The token is a magus surface name, not a phrase: the skill teaches the same
// thing in its own words and rewording it should not fail a gate, but dropping
// the CAPABILITY should.
var guardAdviceSkillCoverage = map[string]string{
	"relock":     "relock",
	"checkpoint": "magus vcs checkpoint",
	"search":     "magus refs",
	"cwd":        "magus where",
}

// TestGuardAdviceHasSkillCoverage keeps the guard's advisories reachable on every
// host, not just the one that can inject them.
//
// An advise reaches the MODEL on Claude Code alone. Codex rejects the
// additionalContext key outright, Cursor's command surface has no channel for a
// non-denial, and OpenCode can only log one for the person. That is those hosts'
// contract and magus cannot widen it - so the guard advisory is a timelier
// delivery of guidance, never its only copy.
//
// The installed skills ARE the common channel: plain files every host reads,
// carrying no host conditionals. So anything the guard would advise has to be in
// one, or three hosts out of four never learn it. That was not true when this was
// written: relockGuardContext existed with `relock` appearing in no skill at all,
// while the OpenCode plugin's own comment claimed "the same guidance ships in the
// installed skills, which is why the skills and the guard say the same things".
//
// Sources rather than installed copies, because the source is what a contributor
// edits and what `magus agent install` regenerates from.
func TestGuardAdviceHasSkillCoverage(t *testing.T) {
	entries, err := os.ReadDir(embeddedSkillDir)
	require.NoError(t, err, "read %s", embeddedSkillDir)

	var corpus strings.Builder
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(embeddedSkillDir, entry.Name(), "SKILL.md"))
		require.NoError(t, err, "read skill %s", entry.Name())
		corpus.Write(body)
	}
	all := corpus.String()

	for advisory, token := range guardAdviceSkillCoverage {
		assert.Contains(t, all, token,
			"the %s advisory teaches %q, and no skill under %s mentions it.\n"+
				"An advise reaches the model on Claude Code only, so a skill is where the other three\n"+
				"hosts learn this. Add it to the skill that owns the topic, or drop the advisory.",
			advisory, token, embeddedSkillDir)
	}
}

func TestSkillsGenerateDeclaresEveryShippedSkill(t *testing.T) {
	body, err := os.ReadFile(rootMagusfile)
	require.NoError(t, err, "read %s", rootMagusfile)

	var globs []string
	for _, m := range skillOutputGlob.FindAllStringSubmatch(string(body), -1) {
		globs = append(globs, m[1])
	}
	require.NotEmpty(t, globs, "%s declares no installed-skill outputs", rootMagusfile)

	entries, err := os.ReadDir(embeddedSkillDir)
	require.NoError(t, err, "read %s", embeddedSkillDir)

	matched := func(path string) bool {
		for _, g := range globs {
			if ok, _ := doublestar.Match(g, path); ok {
				return true
			}
		}
		return false
	}

	dests := []string{".claude/skills", ".agents/skills", ".opencode/skills"}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		for _, dest := range dests {
			// The primary and its always-full twin, in every destination install writes.
			for _, name := range []string{entry.Name(), agent.FullTwinName(entry.Name())} {
				path := dest + "/" + name + "/SKILL.md"
				assert.True(t, matched(path),
					"%s ships %s, but no declared output in %s covers %s.\n"+
						"An undeclared installed skill classifies as hand-editable source, and the merge\n"+
						"driver stops covering it. Add the pattern to skills_generate.",
					embeddedSkillDir, entry.Name(), rootMagusfile, path)
			}
		}
	}

	for _, name := range handAuthoredSkills {
		for _, dest := range dests {
			path := dest + "/" + name + "/SKILL.md"
			assert.False(t, matched(path),
				"%s declares %s as a generated output, but magus never writes it.\n"+
					"describe file will call it generated and the path guard will warn an author off it.",
				rootMagusfile, path)
		}
	}
}

// templatePage names the page each template is embedded in. The guide is a hub
// with a page per host rather than one long page, so a template is embedded
// where its reader already is - and the map is what keeps "embedded somewhere"
// from degrading into "embedded nowhere anyone looks".
//
// The two generic sh templates share a page because two hosts share the files;
// Cursor's and OpenCode's are self-contained and sit with their host.
var templatePage = map[string]string{
	"magus-guard-command.sh": "docs/guides/integrations/agents/guard-templates.md",
	"magus-guard-path.sh":    "docs/guides/integrations/agents/guard-templates.md",
	"magus-guard-observe.sh": "docs/guides/integrations/agents/guard-templates.md",
	"codex-hooks.json":       "docs/guides/integrations/agents/codex.md",
	"cursor-guard.sh":        "docs/guides/integrations/agents/cursor.md",
	"opencode-plugin.ts":     "docs/guides/integrations/agents/opencode.md",
}

// hookTemplates are the artifacts a reader installs. The directory also holds
// the project's own scaffolding (package.json, tsconfig.json, biome.json, the
// lockfile, node_modules) which exists to LINT the templates and is not itself
// something anyone copies into a host - so the list is explicit rather than a
// directory walk that would drag all of it into the guide.
var hookTemplates = []string{
	"magus-guard-command.sh",
	"magus-guard-path.sh",
	// The one template that carries no verdict: it records a path an agent
	// reached and judges nothing, so it declares no guard coverage and owes no
	// parity row. See the note at the top of the file for why that absence is
	// deliberate rather than a hole.
	"magus-guard-observe.sh",
	"codex-hooks.json",
	"cursor-guard.sh",
	"opencode-plugin.ts",
}

type hookSettings struct {
	Hooks struct {
		PreToolUse []struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"PreToolUse"`
	} `json:"hooks"`
}

// TestDogfoodedHookInvokesTheTemplate keeps this repository honest: its own
// guard must run the same file a reader downloads, not a copy that can drift.
//
// The config is TRACKED, so its absence is a failure rather than a skip. It used to be
// per-developer, on the theory that committing one machine's settings.json would ship that
// machine's wiring everywhere - true of a config naming an absolute path, false of this
// one, which names only a repo-relative script that resolves magus from PATH itself.
//
// The skip is what made the change worth making: it fired in every fresh clone and in CI,
// so the one test that checks this repo's own guard wiring had effectively never run. An
// absent guard and a passing suite is the combination worth refusing.
func TestDogfoodedHookInvokesTheTemplate(t *testing.T) {
	raw, err := os.ReadFile(dogfoodedHookConfig)
	require.NoError(t, err, "read %s - it is tracked, so a missing one means this checkout has no guard wired", dogfoodedHookConfig)

	var cfg hookSettings
	require.NoError(t, json.Unmarshal(raw, &cfg), "parse %s", dogfoodedHookConfig)
	require.NotEmpty(t, cfg.Hooks.PreToolUse, "%s declares no PreToolUse hooks", dogfoodedHookConfig)

	for _, entry := range cfg.Hooks.PreToolUse {
		require.NotEmpty(t, entry.Hooks, "matcher %q has no hooks", entry.Matcher)
		for _, h := range entry.Hooks {
			assert.Contains(t, h.Command, hookTemplateDir,
				"the %q hook must invoke a template under %s rather than inline its own copy, "+
					"so dogfooding exercises the file readers download", entry.Matcher, hookTemplateDir)

			// The referenced file must exist: a hook pointing at a moved or
			// renamed template fails open silently, which is the failure mode
			// this whole arrangement exists to remove.
			for _, field := range strings.Fields(h.Command) {
				if strings.HasPrefix(field, hookTemplateDir) {
					assert.FileExists(t, field, "%q hook references a template that does not exist", entry.Matcher)
				}
			}
		}
	}
}

// templateDirScaffolding is what lives beside the templates to LINT them rather
// than to be installed into a host. Everything else in that directory is an
// artifact a reader downloads, and therefore owes the parity gates below.
//
// An allowlist rather than a suffix rule, and deliberately so: the failure mode
// worth preventing is a new HOST arriving unregistered, and this errs toward
// failing on anything unrecognized. Adding tooling here costs one line; adding a
// host without one costs a silently unguarded integration.
var templateDirScaffolding = map[string]bool{
	"package.json":  true,
	"tsconfig.json": true,
	"biome.json":    true,
	// Formats this directory's guide pages. It exists because the parent docs
	// project may not write here (MGS3001), not because a reader installs it.
	"dprint.json":             true,
	"pnpm-lock.yaml":          true,
	"magusfile.buzz":          true,
	"opencode-plugin.test.ts": true,
	// The binary-interface twin: proves the recorded shim's argv shape still gets
	// a real verdict from a real magus, but is not itself something a reader
	// copies into a host - see the note at its top for the split with the file
	// above.
	"opencode-plugin.live.test.ts": true,
}

// TestEveryShippedTemplateIsRegistered closes the gate the other parity tests
// leave open: they all iterate hookTemplates, so an artifact missing from that
// list is not merely untested, it is invisible to every check at once - not
// embedded in the guide, not asked for a coverage declaration, not owed a row in
// the parity table.
//
// That is the exact shape of the regression this whole gate exists to prevent,
// one level up: someone adds a fifth host, wires it correctly, and every test
// stays green while the new integration answers to nothing.
func TestEveryShippedTemplateIsRegistered(t *testing.T) {
	registered := make(map[string]bool, len(hookTemplates))
	for _, name := range hookTemplates {
		registered[name] = true
	}

	entries, err := os.ReadDir(hookTemplateDir)
	require.NoError(t, err, "read %s", hookTemplateDir)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || templateDirScaffolding[name] {
			continue
		}
		switch filepath.Ext(name) {
		case ".sh", ".ts", ".json":
		default:
			continue
		}
		assert.True(t, registered[name],
			"%s ships %s, but hookTemplates does not list it, so every parity check skips it:\n"+
				"the guide is not required to embed it, no coverage declaration is demanded of it,\n"+
				"and the parity table owes it no row. Add it to hookTemplates, or to\n"+
				"templateDirScaffolding if it is tooling rather than something a reader installs.",
			hookTemplateDir, name)
	}
}

// TestShippedTemplatesCarryTheCurrentVersion makes a template version bump
// TOTAL: re-stamp every template, or the build fails.
//
// This is the hook templates' answer to the fingerprint an installed skill
// carries. The two artifacts differ in who owns them - a skill is generated and
// regraded on every `magus doctor`, a template is copied into a host's config
// and owned by its reader from then on - so the marker cannot be a content
// digest without flagging every customization the templates explicitly invite.
// A version survives editing and still answers the one question that matters to
// a reader: is my copy older than the fix?
//
// The forcing function only works if the bump reaches every file. Without this,
// bumping the constant for a fix in one template would leave the other three
// claiming a version whose behavior they do not have, which is worse than no
// marker at all - it would be a wrong answer rather than a missing one.
func TestShippedTemplatesCarryTheCurrentVersion(t *testing.T) {
	want := fmt.Sprintf("%s %d", agent.GuardTemplateMarker, agent.GuardTemplateVersion)
	for _, name := range hookTemplates {
		// codex-hooks.json is JSON: no comment syntax to carry a marker, and an
		// invented key risks the host rejecting the config. It ships no logic of
		// its own - it names the two templates that do - so its version is theirs.
		if filepath.Ext(name) == ".json" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(hookTemplateDir, name))
		require.NoError(t, err, "read %s", name)
		assert.Contains(t, string(body), want,
			"%s does not carry %q.\n"+
				"Every shipped template states the version a reader can compare their own copy against,\n"+
				"and a bump has to reach all of them at once: a file left behind claims a version whose\n"+
				"behavior it does not have, which is a wrong answer rather than a missing one.", name, want)
	}
}

// guardCoverageMarker introduces a template's machine-readable statement of how
// much of a verdict it can carry, on one guard surface, for one or more hosts.
const guardCoverageMarker = "magus-guard-coverage:"

// guardStances are the answers a template may give for a decision: the model
// sees it, only the person sees it, or it is not delivered at all. "none" is a
// legitimate answer - Cursor sends nothing on an allow, so an advise dies there
// - and recording it is the point. Silence is the bug, because silence is
// indistinguishable from nobody having asked.
var guardStances = map[string]bool{"model": true, "human": true, "none": true}

// hostCoverage is host -> surface -> decision -> stance.
type hostCoverage map[string]map[string]map[string]string

// parseGuardCoverage reads every coverage declaration out of the hook templates.
// codex-hooks.json carries none and cannot: it is JSON, comments are not
// available, and an invented key risks the host rejecting the config. Codex's
// coverage is asserted separately, from the templates its command strings
// invoke.
func parseGuardCoverage(t *testing.T) hostCoverage {
	t.Helper()
	cov := hostCoverage{}
	for _, name := range hookTemplates {
		body, err := os.ReadFile(filepath.Join(hookTemplateDir, name))
		require.NoError(t, err, "read %s", name)
		for _, line := range strings.Split(string(body), "\n") {
			_, decl, found := strings.Cut(line, guardCoverageMarker)
			if !found {
				continue
			}
			fields := map[string]string{}
			for _, kv := range strings.Fields(decl) {
				key, value, ok := strings.Cut(kv, "=")
				require.True(t, ok, "%s: coverage declaration field %q is not key=value", name, kv)
				fields[key] = value
			}
			require.Equal(t, "1", fields["schema"],
				"%s declares guard schema %q; the contract is agent.GuardSchemaVersion=%d.\n"+
					"A schema bump means every host glue must be updated and re-downloaded before it guards again.",
				name, fields["schema"], agent.GuardSchemaVersion)
			surface := fields["surface"]
			require.Contains(t, agent.GuardSurfaces(), surface, "%s declares an unknown guard surface %q", name, surface)
			require.NotEmpty(t, fields["host"], "%s: coverage declaration names no host", name)

			for _, host := range strings.Split(fields["host"], ",") {
				if cov[host] == nil {
					cov[host] = map[string]map[string]string{}
				}
				require.Nil(t, cov[host][surface],
					"host %q has two declarations for the %s surface; one artifact per host per surface, or the gate cannot tell which is true", host, surface)
				stances := map[string]string{}
				for _, decision := range agent.GuardDecisions() {
					stance, ok := fields[decision]
					require.True(t, ok,
						"%s declares the %s surface for host %q but says nothing about the %q decision.\n"+
							"Every decision in agent.GuardDecisions needs an explicit stance (model, human, or none):\n"+
							"an undeclared decision is one this host was never asked about.", name, surface, host, decision)
					require.True(t, guardStances[stance], "%s: unknown stance %q for %q (want model, human, or none)", name, stance, decision)
					stances[decision] = stance
				}
				cov[host][surface] = stances
			}
		}
	}
	require.NotEmpty(t, cov, "no %s declarations found in any hook template", guardCoverageMarker)
	return cov
}

// TestHostGluesCoverTheGuardContract is the host-parity gate.
//
// magus's guard rules come from one binary and are identical for every host;
// what differs is how much of a verdict a host's hook surface can carry. That
// difference was recorded only in prose - a table in the guide that nothing
// checked - so adding a decision kind or a guard surface could leave a host
// silently uncovered, and the first person to notice would be a user whose
// session was not guarded.
//
// This makes the contract in internal/agent the thing every artifact answers
// to. A new decision or surface must be added there first (the cmd/magus test
// TestGuardDecisionsCoverEveryVerdictTheHookEmits forces that much), and the
// moment it is, every template owes it an explicit stance and the guide's table
// owes it a column. A glue left untouched fails `go test`.
//
// What it deliberately does NOT check: whether a declaration TELLS THE TRUTH.
// A template that declares advise=model while its response body is mangled
// passes here and fails nowhere - that is transport parity, which needs the
// templates executed against real events, and it is not this test.
func TestHostGluesCoverTheGuardContract(t *testing.T) {
	cov := parseGuardCoverage(t)

	for host, surfaces := range cov {
		for _, surface := range agent.GuardSurfaces() {
			assert.NotNil(t, surfaces[surface],
				"host %q declares no coverage for the %q guard surface.\n"+
					"Either a template wires it and needs a %s line, or the host cannot and\n"+
					"some template must say so - a surface nobody claims is a coverage hole nobody sees.",
				host, surface, guardCoverageMarker)
		}
	}

	// Codex ships no script of its own: codex-hooks.json points at the two
	// generic templates. Those templates claim the codex host, so this is what
	// makes the claim checkable rather than aspirational.
	wiring, err := os.ReadFile(filepath.Join(hookTemplateDir, "codex-hooks.json"))
	require.NoError(t, err)
	for _, name := range hookTemplates {
		body, err := os.ReadFile(filepath.Join(hookTemplateDir, name))
		require.NoError(t, err)
		if !strings.Contains(string(body), "host=codex") && !strings.Contains(string(body), ",codex") {
			continue
		}
		assert.Contains(t, string(wiring), name,
			"%s claims to cover the codex host, but codex-hooks.json never invokes it", name)
	}
}

// failOpenArmRe matches the tests a shipped template makes before answering
// WITHOUT a verdict from magus: the binary is missing or not executable, or it
// ran and left nothing to report. Both spellings the templates use, sh and TS.
var failOpenArmRe = regexp.MustCompile(`! -x "\$GUARD_MAGUS_BIN"|-z "\$verdict"|stdout === null`)

// failOpenRetryRe marks a block that re-invokes magus rather than answering. Two
// of the templates test the same `-z "$verdict"` condition twice - once to retry
// without the attribution flags, once to give up - and only the second is a
// fail-open arm.
var failOpenRetryRe = regexp.MustCompile(`\$\(guard\b|runOnce\(`)

// failOpenNoticeRe matches an arm SAYING it did not judge the call: prose on
// stderr, a console warning, or one of the GUARD_*_RESPONSE envelopes.
var failOpenNoticeRe = regexp.MustCompile(`>&2|console\.warn|unguarded\(\)|\$GUARD_[A-Z_]+_RESPONSE`)

// failOpenOptInRe matches the shape that makes a notice OPT-IN: the arm prints
// only when the reader has set the variable, so by default it prints nothing.
var failOpenOptInRe = regexp.MustCompile(`^\[ -n "\$GUARD_[A-Z_]+" \] &&`)

// failOpenDefaultRe extracts the variable an arm's notice comes from, so the
// default assigned to it can be checked for emptiness.
var failOpenDefaultRe = regexp.MustCompile(`\$(GUARD_[A-Z_]+_RESPONSE)`)

// failOpenSilentByDesign records the verdict-carrying templates whose fail-open
// arms deliberately announce NOTHING, and where that decision is written down.
//
// An exemption rather than a fix, because the decision is recorded with its
// tradeoff named and pinned by an executed case: cmd/magus/testdata/script/
// guard_templates.txtar asserts the path template's silence under a missing
// magus, on the grounds that an empty response on that surface already means
// allow for most hosts and an announcement on every file edit was judged the
// worse noise. Overturning that is a decision for whoever made it; leaving it
// undeclared here is what this table refuses.
var failOpenSilentByDesign = map[string]string{
	"magus-guard-path.sh": "cmd/magus/testdata/script/guard_templates.txtar pins the silence; GUARD_UNAVAILABLE_RESPONSE and GUARD_FAILED_RESPONSE are the opt-in",
}

// TestFailOpenArmsAnnounceThemselves is the doctrine's enforcement point: a
// template that cannot judge a call must SAY so.
//
// Silence and a clean session are the same observation. Every other gate here
// checks what a template does with a verdict it received; this one checks the
// case where it received none, which is the case a reader never notices - the
// guard stops enforcing and the transcript looks exactly as it did before.
//
// Structural on purpose: it finds the arms by the conditions the templates test
// and asks each one for an unconditional notice, so rewording a message costs
// nothing and DELETING one fails. A template with no coverage declaration is not
// asked, which is how magus-guard-observe.sh is exempt - it carries no verdict,
// so it has no fail-open to announce.
func TestFailOpenArmsAnnounceThemselves(t *testing.T) {
	for _, name := range hookTemplates {
		body, err := os.ReadFile(filepath.Join(hookTemplateDir, name))
		require.NoError(t, err, "read %s", name)
		doc := string(body)
		if !strings.Contains(doc, guardCoverageMarker) {
			continue
		}

		lines := strings.Split(doc, "\n")
		arms := 0
		for i, line := range lines {
			if !failOpenArmRe.MatchString(line) {
				continue
			}
			block := lines[i:failOpenArmEnd(lines, i)]
			if failOpenRetryRe.MatchString(strings.Join(block, "\n")) {
				continue
			}
			arms++
			if why, exempt := failOpenSilentByDesign[name]; exempt {
				t.Logf("%s: fail-open arm at line %d is silent by design (%s)", name, i+1, why)
				continue
			}
			assertFailOpenNotice(t, name, doc, block, i+1)
		}
		assert.NotZero(t, arms,
			"%s carries a verdict but no fail-open arm was recognized in it.\n"+
				"Either it now answers some other way - update failOpenArmRe - or it blocks when magus\n"+
				"is unavailable, which is a change this gate should have been told about.", name)
	}
}

// failOpenArmEnd returns the line index just past the block opened at start: the
// next `fi` or bare `}` at any indent. Adequate for these templates, which never
// nest a block inside a fail-open arm.
func failOpenArmEnd(lines []string, start int) int {
	for i := start + 1; i < len(lines); i++ {
		switch strings.TrimSpace(lines[i]) {
		case "fi", "}":
			return i + 1
		}
	}
	return len(lines)
}

// assertFailOpenNotice requires one arm to emit a notice unconditionally, and
// requires whatever variable carries that notice to have a non-empty default.
// The second half is the half that matters: an arm printing a variable nobody
// assigned is silent, and reads like an announcement.
func assertFailOpenNotice(t *testing.T, name, doc string, block []string, line int) {
	t.Helper()
	for _, l := range block {
		trimmed := strings.TrimSpace(l)
		if failOpenOptInRe.MatchString(trimmed) || !failOpenNoticeRe.MatchString(trimmed) {
			continue
		}
		for _, m := range failOpenDefaultRe.FindAllStringSubmatch(trimmed, -1) {
			assert.Regexp(t, `\|\| `+m[1]+`='.+'`, doc,
				"%s prints $%s on its fail-open arm at line %d, but assigns it no non-empty default,\n"+
					"so the arm is silent unless the reader sets it.", name, m[1], line)
		}
		return
	}
	assert.Fail(t, "fail-open arm says nothing",
		"%s answers without a magus verdict at line %d and emits no default notice.\n"+
			"A guard that stopped enforcing looks exactly like a clean session, so every fail-open arm\n"+
			"announces itself (see magus-guard-command.sh's GUARD_UNAVAILABLE_RESPONSE). Add a notice,\n"+
			"or record the arm in failOpenSilentByDesign with where the decision to stay quiet is written.",
		name, line)
}

// transportCorpus is the testscript that executes the sh templates against real
// host events. Its cases are labeled so the contract can demand one per cell.
const transportCorpus = "cmd/magus/testdata/script/guard_templates.txtar"

// TestTransportCorpusCoversTheContract ties the executed cases to the same
// contract the declarations answer to.
//
// Coverage parity asks every glue to DECLARE a stance; this asks that somebody
// actually ran it. Without this, growing the contract would demand new
// declarations (which the sibling test enforces) while the transport corpus
// quietly kept testing the old cells - and a declaration nobody executes is the
// exact failure that let a broken plugin ship.
//
// A cell the guard cannot currently produce is declared rather than skipped:
// `# case: path/deny unreachable - ...` satisfies this and says why in the file
// where the next person will look.
func TestTransportCorpusCoversTheContract(t *testing.T) {
	body, err := os.ReadFile(transportCorpus)
	require.NoError(t, err, "read %s", transportCorpus)
	corpus := string(body)

	for _, surface := range agent.GuardSurfaces() {
		for _, decision := range agent.GuardDecisions() {
			label := "# case: " + surface + "/" + decision
			assert.Contains(t, corpus, label,
				"%s has no case labeled %q.\n"+
					"Every surface-and-decision pair in the guard contract needs one executed case, or an\n"+
					"explicit `%s unreachable - <why>` line when the guard cannot produce that verdict.",
				transportCorpus, label, label)
		}
	}
}

// TestParityTableMatchesTheGlueDeclarations keeps the guide's hand-written
// "Parity across hosts" table honest against the declarations.
//
// The table is what a person reads before choosing a host, so a wrong cell is a
// user picking a host on a promise magus does not keep. Its deny and advise
// columns describe the COMMAND surface - the declared-output rule has its own
// column - so that is what they are checked against.
func TestParityTableMatchesTheGlueDeclarations(t *testing.T) {
	cov := parseGuardCoverage(t)
	guide, err := os.ReadFile(hookGuideDoc)
	require.NoError(t, err)

	rows := parityTableRows(t, string(guide))
	for host, surfaces := range cov {
		cells, ok := rows[normalizeHost(host)]
		require.True(t, ok,
			"host %q is declared by a template but has no row in the parity table in %s.\n"+
				"Every supported host belongs in the table a reader uses to choose one.", host, hookGuideDoc)

		assertCell := func(column string, want bool) {
			t.Helper()
			cell, ok := cells[column]
			require.True(t, ok, "the parity table has no %q column; the contract needs one", column)
			got := strings.HasPrefix(strings.ToLower(cell), "yes")
			assert.Equal(t, want, got,
				"parity table row %q, column %q reads %q, which disagrees with the coverage the templates declare.\n"+
					"Fix whichever is wrong - the table is a promise to a reader, the declaration is what the file does.",
				host, column, cell)
		}
		assertCell("command rules", surfaces["command"] != nil)
		assertCell("declared-output rule", surfaces["path"] != nil)
		if command := surfaces["command"]; command != nil {
			assertCell("deny", command["deny"] == "model")
			assertCell("advise", command["advise"] == "model")
		}
	}

	var declared []string
	for host := range cov {
		declared = append(declared, normalizeHost(host))
	}
	sort.Strings(declared)
	for row := range rows {
		assert.Contains(t, declared, row,
			"the parity table in %s promises host %q, which no template declares coverage for", hookGuideDoc, row)
	}
}

// normalizeHost folds the two spellings of a host name - the declarations use
// the label the guard records (claude-code), the guide uses the product's own
// (Claude Code) - so neither has to bend to the other.
func normalizeHost(s string) string {
	return strings.NewReplacer(" ", "", "-", "", "`", "").Replace(strings.ToLower(strings.TrimSpace(s)))
}

// parityTableRows extracts the guide's parity table as row label -> column ->
// cell. Columns are keyed by the distinguishing word in the header rather than
// its full text, so rewording a header does not silently drop a check.
func parityTableRows(t *testing.T, guide string) map[string]map[string]string {
	t.Helper()
	// Cut on the heading text rather than its level: the section moved up a level
	// when the guide became a hub, and a heading level is not what this gate is about.
	_, section, found := strings.Cut(guide, "Parity across hosts")
	require.True(t, found, "%s has no 'Parity across hosts' section; the parity table is the human half of this gate", hookGuideDoc)

	var header []string
	rows := map[string]map[string]string{}
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			if len(rows) > 0 || header != nil {
				break // past the table
			}
			continue
		}
		cells := splitTableRow(line)
		switch {
		case header == nil:
			for _, cell := range cells {
				switch {
				case strings.Contains(cell, "command rules"):
					header = append(header, "command rules")
				case strings.Contains(cell, "declared-output"):
					header = append(header, "declared-output rule")
				case strings.Contains(cell, "deny"):
					header = append(header, "deny")
				case strings.Contains(cell, "advise"):
					header = append(header, "advise")
				default:
					header = append(header, cell)
				}
			}
		case strings.HasPrefix(cells[0], "---"):
		default:
			row := map[string]string{}
			for i, cell := range cells {
				if i < len(header) && i > 0 {
					row[header[i]] = cell
				}
			}
			rows[normalizeHost(cells[0])] = row
		}
	}
	require.NotEmpty(t, rows, "parsed no rows out of the parity table in %s", hookGuideDoc)
	return rows
}

func splitTableRow(line string) []string {
	parts := strings.Split(strings.Trim(line, "|"), "|")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

// TestHookTemplatesAreEmbeddedInTheGuide keeps the docs site explorable without
// a transclusion feature: every template is embedded verbatim, and an edit to
// one that is not mirrored into its page fails here.
func TestHookTemplatesAreEmbeddedInTheGuide(t *testing.T) {
	for _, name := range hookTemplates {
		page, ok := templatePage[name]
		require.True(t, ok,
			"%s is shipped but templatePage says nothing about where it is embedded, "+
				"so a reader browsing the docs site has no way to reach it", name)
		guide, err := os.ReadFile(page)
		require.NoError(t, err, "read %s", page)
		doc := string(guide)

		body, err := os.ReadFile(filepath.Join(hookTemplateDir, name))
		require.NoError(t, err, "every template in hookTemplates must exist")

		// Compare on the executable lines only. Comments carry the reasoning and
		// are worth reading in the file itself; requiring the guide to mirror
		// every one of them would make the page unreadable and the test brittle.
		var missing []string
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
				continue
			}
			if !strings.Contains(doc, line) {
				missing = append(missing, line)
			}
		}
		assert.Empty(t, missing,
			"%s embeds %s incompletely: the lines below are in the template but not on that page.\n"+
				"Re-copy the template into its code block - a reader exploring the docs site must see\n"+
				"what they would download.", page, name)
	}
}

// rootOnlyMagusLookup matches resolving magus as `./magus` - the workspace root's copy,
// and only when the process already stands in the root.
var rootOnlyMagusLookup = regexp.MustCompile(`-x\s+\.?/?\./magus\b|-x\s+"\./magus"`)

// TestGuideExamplesDoNotResolveMagusFromTheProcessDirectory covers the copy-paste hook
// commands that are inline JSON rather than shipped files.
//
// They carry no `magus-guard-template:` marker and cannot: the marker is graded by
// reading the file a host config points at, and an inline command is not a file. So
// neither the staleness check nor TestHookTemplatesAreEmbeddedInTheGuide sees them, and
// the resolution logic they duplicate drifted out of step with the templates exactly
// once already - the templates learned to walk up to the magusfile and these did not,
// which is silent: the hook still exits 0 and the events just stop arriving.
//
// Asserted over EVERY page, not just the two commands that had the bug, because the
// next copy of this pattern will be pasted somewhere else.
func TestGuideExamplesDoNotResolveMagusFromTheProcessDirectory(t *testing.T) {
	pages, err := filepath.Glob(filepath.Join(hookTemplateDir, "*.md"))
	require.NoError(t, err)
	require.NotEmpty(t, pages, "the agent guide pages must be discoverable")

	for _, page := range pages {
		body, err := os.ReadFile(page)
		require.NoError(t, err, "read %s", page)
		for i, line := range strings.Split(string(body), "\n") {
			assert.NotRegexp(t, rootOnlyMagusLookup, line,
				"%s:%d resolves magus as ./magus, which only finds it when the hook happens to\n"+
					"run in the workspace root. Walk up to the magusfile instead, the way the shipped\n"+
					"templates do:\n"+
					`  d=$PWD; while [ -n "$d" ] && [ ! -f "$d/magusfile.buzz" ]; do d=${d%%/*}; done`,
				page, i+1)
		}
	}
}

// rootSourcesBlock captures the root project's declared source globs: everything
// between `"sources": [` and the closing bracket. One `"sources"` key exists in this
// magusfile (the root project's), so the first match is it.
var rootSourcesBlock = regexp.MustCompile(`(?s)"sources":\s*\[(.*?)\]`)

// quotedString matches one glob inside that block, comments excluded by construction
// (a trailing `// ...` carries no quotes).
var quotedString = regexp.MustCompile(`"([^"]+)"`)

// TestRootProjectDeclaresTheConfigsItsToolsRead is this repository declining to be the
// example in its own diagnostic.
//
// Each path below changes what a root target DOES while matching no spell source glob,
// so it has to be declared or an edit to one produces a cache HIT - golangci-lint re-run
// under a new rule set, replaying the verdict computed under the old one - while still
// seeding the root project through directory containment and rerunning everything for
// nothing (MGS1028, and doctor's undeclared-seeding advice).
//
// Deliberately absent, and this test says so rather than leaving it to be rediscovered:
// LICENSE is read only by release-build, which is skip_cache, so declaring it would
// invalidate build/test/lint to key nothing; .gitignore is read by no target at all.
// Both keep seeding by containment, correctly.
func TestRootProjectDeclaresTheConfigsItsToolsRead(t *testing.T) {
	body, err := os.ReadFile(rootMagusfile)
	require.NoError(t, err, "read %s", rootMagusfile)

	block := rootSourcesBlock.FindStringSubmatch(string(body))
	require.Len(t, block, 2, "%s declares no project-wide sources", rootMagusfile)
	var declared []string
	for _, m := range quotedString.FindAllStringSubmatch(block[1], -1) {
		declared = append(declared, m[1])
	}

	for path, reader := range map[string]string{
		".golangci.yml":       "golangci-lint, through the go spell's lint op",
		".mockery.yaml":       "`go tool mockery`, in the generate target",
		".markdownlintignore": "markdownlint, through the markdown spell's lint op",
		"dprint-base.json":    "dprint (dprint.json extends it; the spell declares only dprint.json)",
		"mise.toml":           "every op in this project, through the toolchain it pins",
		"magus.yaml":          "every run, through the charm/cache/sandbox policy it resolves",
		"Dockerfile":          "image-build's amd64 variant",
		"Dockerfile.static":   "image-build's static multi-arch variant",
	} {
		assert.Contains(t, declared, path,
			"%s is read by %s but no root source glob names it, so editing it reruns every\n"+
				"root target while keying none of them. Declare it in this magusfile's sources.",
			path, reader)
	}
}

// wholeTreeFootprints are the root targets that compile or analyze the entire Go
// module, mapped to the globs whose absence from the target's own footprint would make
// a Go edit replay instead of re-measure. Two entries because two targets run the whole
// tree; every other readsFiles in this magusfile narrows deliberately (compress-cgo-test
// to one package, lint-build to libs/testlayout) and must keep its narrow footprint.
var wholeTreeFootprints = map[string][]string{
	"test": {"**/*.go", "go.mod", "go.sum"},
	"lint": {"**/*.go", "go.mod", "go.sum", ".golangci.yml"},
}

// TestWholeTreeTargetsKeyOnTheGoTree pins the footprint replacement that made this
// repository's own suite report a false green.
//
// ctx.readsFiles REPLACES a target's footprint rather than adding to it: buildStep keeps
// the magusfiles and the target's spell sources, drops the project and spell globs, then
// folds the declared refs in. So a call added to key four files the project does not
// claim - the console palette that types/kindpalette_drift_test.go reads - silently
// deleted **/*.go from the key of the target that runs every Go test. It held long enough
// for ~8,400 new lines of *_test.go to land and replay against the cached verdict with the
// coverage profile untouched; only --no-cache re-measured. `magus affected` still selected
// "." for those edits, because project claims and a target's key are computed separately,
// which is what kept it invisible.
//
// Structural rather than a text scan: it reads the same static extraction buildStep
// depends on, so a footprint moved into a helper or reworded still gets graded.
func TestWholeTreeTargetsKeyOnTheGoTree(t *testing.T) {
	body, err := os.ReadFile(rootMagusfile)
	require.NoError(t, err, "read %s", rootMagusfile)

	nodes := describe.Extract(string(body))
	require.NotEmpty(t, nodes, "%s yielded no target nodes; the parse failed", rootMagusfile)

	byName := make(map[string]types.TargetGraphNode, len(nodes))
	for _, n := range nodes {
		byName[n.Name] = n
	}

	for target, required := range wholeTreeFootprints {
		node, ok := byName[target]
		require.True(t, ok, "%s declares no %q target", rootMagusfile, target)
		if len(node.ReadsFiles) == 0 {
			continue // no replacement, so the target still inherits the project baseline
		}
		var declared []string
		for _, ref := range node.ReadsFiles {
			declared = append(declared, ref.Glob)
		}
		for _, glob := range required {
			assert.Contains(t, declared, glob,
				"target %q declares a ctx.readsFiles footprint, which REPLACES the project and\n"+
					"spell source globs, but does not name %q - so an edit to it produces a cache HIT\n"+
					"and %q replays a verdict computed against different code. Restate the tree the\n"+
					"target actually reads, or drop the footprint declaration entirely.",
				target, glob, target)
		}
	}
}

// magus is agent-host agnostic, and this test is the only thing that enforces it.
// The rule was written down twice - in docs/guides/integrations/agents.md ("magus
// owns the guard rules and the verdict, not integration code for each host") and
// in the skill-authoring skill ("no host name appears in code") - and honored
// nowhere mechanically, which is how `magus mcp` came to print Codex and Claude
// Desktop setup instructions. A change to any one of those clients then meant a
// magus release.
//
// The rule, precisely: a host's NAME may appear only as part of a filesystem
// path, because naming the directory a host discovers skills in is the one
// host-specific step magus is allowed to know about (agents.md says so). Anywhere
// else - prose, help text, printed setup instructions, a per-host branch - the
// host-specific part belongs in documentation the reader owns.

// hostNames are agent hosts magus must not encode behavior for. Deliberately
// omits "cursor": magus uses it as an ordinary pagination term, so matching it
// would be noise rather than signal.
var hostNames = regexp.MustCompile(`(?i)\b(claude|opencode|codex|aider|windsurf)\b`)

// hostPathUse allows a host name that names something ON DISK: a path
// (`.claude/skills`, `~/.config/opencode/skills`, `.codex/config.toml`) or a bare
// quoted filename stem (`case "agents", "claude":` classifying AGENTS.md and
// CLAUDE.md). Recognizing a well-known file is the sanctioned exception - it is a
// destination, not a code path branching on which host is running.
var hostPathUse = regexp.MustCompile(`(?i)([./~][a-z0-9_.-]*\b(claude|opencode|codex|aider|windsurf)\b[a-z0-9_.-]*)|("(claude|opencode|codex|aider|windsurf)")`)

// hostAgnosticSkipDirs are trees this rule does not govern: generated output,
// vendored/third-party code, and the embedded skill bodies (which are
// documentation, and already ASCII- and drift-checked elsewhere).
var hostAgnosticSkipDirs = map[string]bool{
	".git": true, ".magus": true, ".claude": true, ".agents": true, ".opencode": true,
	"node_modules": true, "gen": true, "testdata": true, "docs": true, "blog": true,
	"skills": true, "releases": true, "manpage": true, "schema": true,
}

func TestNoHostSpecificBehaviorInCode(t *testing.T) {
	var violations []string

	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if hostAgnosticSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		// Tests may name a host: a test asserting the guard's behavior against a
		// real host event is describing the world, not encoding a code path.
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()

		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for line := 1; sc.Scan(); line++ {
			text := sc.Text()
			if !hostNames.MatchString(text) {
				continue
			}
			// Strip every path-shaped use, then re-test: a line may legitimately
			// carry both (an example destination plus surrounding prose).
			if !hostNames.MatchString(hostPathUse.ReplaceAllString(text, "")) {
				continue
			}
			violations = append(violations, fmt.Sprintf("%s:%d: %s", path, line, strings.TrimSpace(text)))
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	assert.Empty(t, violations,
		"magus must not encode agent-host specifics.\n"+
			"A host name is allowed only inside a filesystem path (e.g. .claude/skills), because naming\n"+
			"the directory a host reads is the one host-specific step magus owns. Everything else - setup\n"+
			"instructions, help text, a per-host branch - belongs in docs the reader owns, or the next\n"+
			"change to that host becomes a magus release.\n\nviolations:\n%s",
		strings.Join(violations, "\n"))
}

// The test above is one layer shallower than the rule it enforces. A branch keyed
// on a host's TOOL VOCABULARY rather than its name - `switch tool { case "Read":
// ... case "Bash": }` - is a per-host branch in everything but spelling, and no
// host name appears in it, so the name scan waves it through. The test below is
// that second layer.

// hostToolVocabularyScope is where this rule bites: the guard's own source, the
// only code that ever sees a host's hook payload. Scoped rather than tree-wide
// because "Read", "Write" and "Task" are ordinary words elsewhere in this module -
// method names, struct fields, JSON tags, graph kinds, spell ops - and a tree-wide
// scan would report hundreds of them and be turned off within the week. A per-host
// branch that is not deciding a verdict is not the failure this exists to prevent.
var hostToolVocabularyScope = []string{
	filepath.Join("cmd", "magus", "guard*.go"),
	filepath.Join("internal", "agent", "*.go"),
}

// hostToolVocabulary is what agent hosts call their tools. magus's own labels -
// hookToolCommand, hookToolWrite, hookToolRead in cmd/magus/guard.go - are
// deliberately none of these, and are the positive example: a wrapper maps its
// host's name to magus's label by which flag it passes, so a host renaming a tool
// costs its reader one config line instead of costing magus a release.
var hostToolVocabulary = map[string]bool{
	"Read": true, "Write": true, "Edit": true, "MultiEdit": true, "NotebookEdit": true,
	"Glob": true, "Grep": true, "Bash": true, "Task": true, "TodoWrite": true,
	"WebFetch": true, "WebSearch": true, "ExitPlanMode": true,
	"read_file": true, "write_file": true, "edit_file": true, "list_dir": true,
	"apply_patch": true, "run_terminal_cmd": true, "str_replace_editor": true,
	"codebase_search": true, "shell": true,
}

// hostToolVocabularyByDesign is the escape hatch, in the shape
// failOpenSilentByDesign uses: "<path>:<literal>" mapped to WHERE the decision to
// write a host's word into guard code is recorded. There is no per-line exemption
// comment in this repo, so an entry here - reviewable, and readable as a list - is
// the only way past this gate.
//
// Empty, and expected to stay that way: the wire contract in
// internal/agent/guard.go is magus's vocabulary end to end. It exists so the next
// author has somewhere to put the argument rather than somewhere to hide it.
var hostToolVocabularyByDesign = map[string]string{}

// TestGuardDoesNotBranchOnHostToolVocabulary rejects a host's tool NAME appearing
// as a string literal anywhere in guard code, not merely in a comparison.
//
// The literal is the whole signal. There is no innocent reason for the guard to
// spell a host's word for "read a file", and reading only == and case clauses
// would pass a `map[string]surface{"Read": ...}`, which is the same branch with
// the dispatch moved into a table. AST rather than a text scan so the prose that
// explains the rule - including guard.go's own comment, which quotes "Read" and
// "Bash" - is not itself a violation.
func TestGuardDoesNotBranchOnHostToolVocabulary(t *testing.T) {
	var violations []string

	fset := token.NewFileSet()
	for _, glob := range hostToolVocabularyScope {
		paths, err := filepath.Glob(glob)
		require.NoErrorf(t, err, "glob %s", glob)
		require.NotEmptyf(t, paths, "%s matched no files; the guard moved and this gate stopped looking", glob)

		for _, path := range paths {
			if strings.HasSuffix(path, "_test.go") {
				continue // a test may name a host's tool: it describes a real payload rather than judging one
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			require.NoErrorf(t, err, "parse %s", path)

			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil || !hostToolVocabulary[value] {
					return true
				}
				if why, exempt := hostToolVocabularyByDesign[path+":"+value]; exempt {
					t.Logf("%s: %q is host vocabulary by design (%s)", path, value, why)
					return true
				}
				pos := fset.Position(lit.Pos())
				violations = append(violations, fmt.Sprintf("%s:%d: %q", path, pos.Line, value))
				return true
			})
		}
	}

	assert.Empty(t, violations,
		"guard code must not know what a host calls its tools.\n"+
			"A switch or a lookup table over \"Read\"/\"Bash\" is a per-host branch with the host's name\n"+
			"filed off: it passes TestNoHostSpecificBehaviorInCode, and the next time any host renames a\n"+
			"tool it costs a magus release. Record magus's own label instead (hookToolCommand,\n"+
			"hookToolWrite, hookToolRead in cmd/magus/guard.go) and let the wrapper in the reader's own\n"+
			"config do the mapping - which flag it passes IS the mapping. If a literal genuinely has to\n"+
			"be here, add it to hostToolVocabularyByDesign with where that decision is written down.\n\n"+
			"violations:\n%s",
		strings.Join(violations, "\n"))
}

// The landing headline rotates through N stacked spans on one shared keyframe
// animation, each offset by a negative delay of one slot. The count lives in
// landing.buzz and the slot arithmetic lives in site.css, so nothing but agreement
// between two files keeps them in step - and that agreement broke: a thirteenth
// line was added while site.css still had delays for twelve.
//
// The failure is invisible in review and ugly in production. A span with no delay
// rule does not lose its animation, it inherits delay 0 and rides the FIRST span's
// timeline, so two headlines fade in stacked on each other and the <h1> renders as
// interleaved glyphs. Counting is what a reader cannot do reliably; assert it.
func TestLandingRotatorSlotsMatchHeadlineCount(t *testing.T) {
	t.Parallel()
	markup, err := os.ReadFile(filepath.Join("docs", "site", "landing.buzz"))
	require.NoError(t, err, "read landing.buzz")
	headlines := strings.Count(string(markup), `class="landing-rotate"`)
	require.NotZero(t, headlines, "no rotating headlines found; did the markup change?")

	styles, err := os.ReadFile(filepath.Join("docs", "src", "styles", "site.css"))
	require.NoError(t, err, "read site.css")

	// The first span needs no rule: its slot is delay 0. So the delays cover 2..N,
	// and the highest selector must be exactly N.
	rules := regexp.MustCompile(`\.landing-rotate:nth-child\((\d+)\) \{ animation-delay: -(\d+)s; \}`).
		FindAllStringSubmatch(string(styles), -1)
	delay := map[int]int{1: 0}
	highest := 1
	for _, m := range rules {
		n, convErr := strconv.Atoi(m[1])
		require.NoError(t, convErr)
		d, convErr := strconv.Atoi(m[2])
		require.NoError(t, convErr)
		delay[n] = d
		if n > highest {
			highest = n
		}
	}

	var uncovered []int
	for i := 1; i <= headlines; i++ {
		if _, ok := delay[i]; !ok {
			uncovered = append(uncovered, i)
		}
	}
	assert.Emptyf(t, uncovered,
		"landing.buzz has %d headlines but site.css declares no animation-delay for span(s) %v; "+
			"each falls back to delay 0 and renders on top of the first headline",
		headlines, uncovered)
	assert.Equalf(t, headlines, highest,
		"site.css's highest .landing-rotate:nth-child(%d) does not match landing.buzz's %d headlines",
		highest, headlines)

	// The slot length is a design choice, so read it from the CSS (span 2's offset IS
	// one slot) rather than pinning a number here. What must hold is the arithmetic
	// around it: every span sits one slot further along, and the cycle is exactly one
	// slot per headline. Either failing puts a span somewhere other than its own slot.
	slot := delay[2]
	require.NotZerof(t, slot, "span 2 has no animation-delay; cannot derive the slot length")

	for i := 2; i <= headlines; i++ {
		if d, ok := delay[i]; ok {
			assert.Equalf(t, (i-1)*slot, d,
				"span %d is offset -%ds; one slot is %ds, so it must be -%ds", i, d, slot, (i-1)*slot)
		}
	}

	dur := regexp.MustCompile(`animation: landing-rotate (\d+)s`).FindStringSubmatch(string(styles))
	require.NotNil(t, dur, "no landing-rotate animation duration in site.css")
	assert.Equalf(t, strconv.Itoa(headlines*slot), dur[1],
		"landing-rotate runs %ss for %d headlines at %ds a slot; it must be %ds",
		dur[1], headlines, slot, headlines*slot)
}

// nonASCIIGlyphs are the punctuation substitutes user-facing strings must not
// carry (CLAUDE.md: "user-facing message strings are plain ASCII"). Named
// rather than a blanket >127 check, because a blanket check would also flag
// the deliberate drawing glyphs excluded below.
var nonASCIIGlyphs = map[rune]string{
	'—': "em dash",
	'–': "en dash",
	'‘': "left single quote",
	'’': "right single quote",
	'“': "left double quote",
	'”': "right double quote",
	'→': "right arrow",
	'↔': "left-right arrow",
	'…': "ellipsis",
	'≥': "greater-or-equal sign",
	'≤': "less-or-equal sign",
	'×': "multiplication sign",
	'·': "middle dot",
}

// asciiScanFiles is the exact set of non-test Go sources this pass swept for
// the glyphs above (the P2-19/20 audit). It is a file list rather than a
// package walk on purpose: cmd/magus, status.go's box-drawing pool display and
// internal/cache/log.go's log-preview divider deliberately keep non-ASCII
// glyphs (spinner frames, "|"-drawn borders), and files this pass did not
// touch may carry pre-existing drift this pass was not scoped to fix. Scanning
// exactly the fixed files still catches the regression this test exists for:
// reverting any one fix here fails it.
var asciiScanFiles = []string{
	"types/describe.go",
	"internal/render/targetgraph.go",
	"cmd/magus-docs/main.go",
	"internal/observability/otlp/provider.go",
	"internal/handler/mcp/registry.go",
	"internal/handler/mcp/output.go",
	"internal/handler/mcp/where.go",
	"cmd/magus/query.go",
	"cmd/magus/guard_shell.go",
	"cmd/magus/guard_write.go",
	"cmd/magus/config_console.go",
	"internal/doctor/checks.go",
	"cmd/magus/init.go",
	"internal/config/load.go",
	"internal/config/validate.go",
	"internal/interp/repl.go",
	"internal/interp/bindings/pry.go",
	"std/platform.go",
	"std/env.go",
	"std/markdown.go",
	"std/buzz_stdlib.go",
	"std/archive.go",
	"std/buzz_signature.go",
	"internal/cache/output.go",
}

// TestUserFacingStringsAreASCII scans string literals (not comments - CLAUDE.md
// exempts those) in asciiScanFiles for the glyphs in nonASCIIGlyphs. It is scoped
// to files this pass swept, not entire packages: see asciiScanFiles's comment.
func TestUserFacingStringsAreASCII(t *testing.T) {
	var violations []string

	fset := token.NewFileSet()
	for _, path := range asciiScanFiles {
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		require.NoErrorf(t, err, "parse %s", path)

		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			for _, r := range lit.Value {
				if name, bad := nonASCIIGlyphs[r]; bad {
					pos := fset.Position(lit.Pos())
					violations = append(violations, fmt.Sprintf("%s:%d: %s in %s", path, pos.Line, name, lit.Value))
				}
			}
			return true
		})
	}

	assert.Empty(t, violations,
		"user-facing strings must be plain ASCII (CLAUDE.md): no em/en dash, curly quotes, arrows, "+
			"ellipsis, >=/<= glyphs, or multiplication sign. Use the ASCII spelling instead (-, ->, "+
			"<->, ..., >=, <=, x).\n\nviolations:\n%s",
		strings.Join(violations, "\n"))
}

// TestTargetIsTheTaughtNoun pins the worst vocabulary-drift regression: the
// printed target definition and the magus-run skill's opening both teaching
// "target" as the unit of work, rather than sliding back to "operation" or
// "task" (docs/concepts/targets.md bans both as a Target substitute). This is
// deliberately narrow - a broad synonym grep false-positives too easily - so it
// only pins these two known-worst sites.
func TestTargetIsTheTaughtNoun(t *testing.T) {
	lower := strings.ToLower(types.TargetDefinition)
	assert.Contains(t, lower, "target", "TargetDefinition must teach target as the unit of work")
	assert.NotContains(t, lower, "operation", "TargetDefinition must not substitute operation for target")
	assert.NotContains(t, lower, "task", "TargetDefinition must not substitute task for target")

	data, err := os.ReadFile(filepath.Join("internal", "agent", "skills", "magus-run", "SKILL.md"))
	require.NoError(t, err, "read magus-run SKILL.md")
	paragraphs := strings.SplitN(string(data), "\n\n", 3)
	require.GreaterOrEqual(t, len(paragraphs), 2, "SKILL.md must have an opening paragraph after its heading")
	opening := strings.ToLower(paragraphs[1])

	assert.Contains(t, opening, "target", "the magus-run skill opening must teach target as the unit of work")
	assert.NotContains(t, opening, "operation", "the magus-run skill opening must not substitute operation for target")
	// "task orchestrator" is the blessed product-positioning phrase (README.md uses it);
	// strip it before checking, so this pins task NOT being used as the taught noun
	// without banning the positioning phrase itself.
	withoutBlessedPhrase := strings.ReplaceAll(opening, "task orchestrator", "")
	assert.NotContains(t, withoutBlessedPhrase, "task",
		"the magus-run skill opening must not teach task as the unit of work (task orchestrator excepted)")
}

// mgsDeclarationFile declares and enumerates every diagnostic code. References there are
// the code EXISTING, not the code firing, so the raise-site scan looks past this one file.
const mgsDeclarationFile = "types/diagnostic.go"

// mgsCodeRe matches a diagnostic code written out as text, which is how a Buzz spell
// raises one - it throws the string, having no Go constant to reach for.
var mgsCodeRe = regexp.MustCompile(`MGS[0-9]{4}`)

// raiseSiteSkipDirs are trees a raise site cannot live in: version control and cache
// state, generated output, fixtures, and vendored code.
var raiseSiteSkipDirs = map[string]bool{
	".git": true, ".magus": true, ".claude": true, ".agents": true, ".opencode": true,
	"node_modules": true, "gen": true, "testdata": true,
}

// mgsCodesWithoutRaiseSite are the codes that fail TestEveryDiagnosticCodeHasARaiseSite
// today, listed rather than tolerated so the gate is green and the debt is named.
//
// TODO: both are undecided. Either the sandbox learns to report what it did - stripping an
// env var, refusing a binary that a PATH entry substituted - or the codes come out of the
// enumeration and out of the docs that promise them. Whoever settles that deletes the entry
// here; nothing else has to change.
var mgsCodesWithoutRaiseSite = map[types.DiagnosticCode]string{
	types.EnvStripped:       "the sandbox strips variables but names no code when it does",
	types.PathShimSuspected: "nothing detects a substituted binary on PATH yet",
}

// TestEveryDiagnosticCodeHasARaiseSite pins the property the code registry silently lost:
// a code magus can never emit.
//
// An enumerated code is a promise - it becomes a knowledge-graph node, a docs page, and a
// string a reader is told to search for. A code with no production raise site keeps every
// one of those and honours none: the page exists, the node exists, and the condition it
// describes reports as something else or as nothing. Nothing at runtime can observe the
// absence, which is why it is asserted over the source shape instead.
//
// Declaring the code in a doctor check registry counts, since that routes a real finding.
// Naming it only in a test does not: a code no shipped path reaches is the defect.
func TestEveryDiagnosticCodeHasARaiseSite(t *testing.T) {
	fset := token.NewFileSet()
	decl, err := parser.ParseFile(fset, mgsDeclarationFile, nil, 0)
	require.NoError(t, err, "parse %s", mgsDeclarationFile)

	// identifier per code, so the scan can look for the Go name a raise site would use
	// rather than the string literal, which almost nothing writes out.
	name := map[types.DiagnosticCode]string{}
	for _, d := range decl.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, s := range gd.Specs {
			vs, ok := s.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			code, err := strconv.Unquote(lit.Value)
			if err != nil {
				continue
			}
			name[types.DiagnosticCode(code)] = vs.Names[0].Name
		}
	}

	raised := map[string]bool{}
	err = filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if raiseSiteSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		slash := filepath.ToSlash(path)
		// The built-in spells are shipped code too, and one of them is the only thing that
		// raises MGS1016 - a spell throws the code as a STRING, so there is no identifier
		// to find. Only spells/: every other .buzz naming a code is a tour page or a
		// glossary entry describing one, which is the opposite of raising it.
		if strings.HasSuffix(slash, ".buzz") {
			if strings.HasPrefix(slash, "spells/") {
				body, rerr := os.ReadFile(path)
				if rerr != nil {
					return rerr
				}
				for _, code := range mgsCodeRe.FindAllString(string(body), -1) {
					if id, ok := name[types.DiagnosticCode(code)]; ok {
						raised[id] = true
					}
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if slash == mgsDeclarationFile {
			return nil
		}
		f, perr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if perr != nil {
			return nil //nolint:nilerr // a file that does not parse is the build's finding, not this gate's
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				raised[id.Name] = true
			}
			return true
		})
		return nil
	})
	require.NoError(t, err, "walk")

	var unraised []string
	for _, code := range types.AllDiagnosticCodes() {
		id, ok := name[code]
		require.True(t, ok, "code %s is enumerated but declared by no constant", code)
		if raised[id] {
			assert.NotContains(t, mgsCodesWithoutRaiseSite, code,
				"%s (%s) now has a raise site; drop it from mgsCodesWithoutRaiseSite", code, id)
			continue
		}
		if why, excepted := mgsCodesWithoutRaiseSite[code]; excepted {
			t.Logf("known exception %s (%s): %s", code, id, why)
			continue
		}
		unraised = append(unraised, fmt.Sprintf("%s (%s)", code, id))
	}
	sort.Strings(unraised)

	assert.Empty(t, unraised,
		"every enumerated diagnostic code must have at least one production raise site.\n"+
			"A code nothing emits still ships a docs page, a graph node, and a promise that the\n"+
			"condition will be reported under it. Raise it where the condition is detected, or\n"+
			"remove it from types.allDiagnosticCodes.\n\nunraised:\n%s",
		strings.Join(unraised, "\n"))
}
