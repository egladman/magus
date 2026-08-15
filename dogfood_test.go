package magus

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

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus/internal/agent"
	json "github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	dogfoodedHookConfig = ".claude/settings.json"
	hookGuideDoc        = "docs/guides/integrations/agents.md"
	hookTemplateDir     = "docs/guides/integrations/agents"

	// rootMagusfile carries skills_generate's declared outputs, which decide both
	// what `magus describe file` calls generated and what EnsureMergeDriver writes
	// into .gitattributes.
	rootMagusfile = "magusfile.buzz"
	// embeddedSkillDir is the shipped set: one directory per skill magus installs.
	embeddedSkillDir = "cmd/magus/skills"
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
// regraded on every `graph verify`, a template is copied into a host's config
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
