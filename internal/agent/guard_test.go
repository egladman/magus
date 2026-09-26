package agent

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"text/template"

	sprig "github.com/Masterminds/sprig/v3"
	"github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// repoRoot is the workspace root as seen from this package's directory, where go test
// runs. The shipped templates, host configs and vendored schemas these tests grade
// live outside any Go package, so no embed can reach them.
const repoRoot = "../.."

const (
	// hookTemplateDir holds the templates a reader installs into an agent host.
	hookTemplateDir = repoRoot + "/docs/guides/integrations/agents"
	// dogfoodedHookConfig is this repository's own Claude Code wiring of those templates.
	dogfoodedHookConfig = repoRoot + "/.claude/settings.json"
)

// hookTemplates are the artifacts a reader installs. The directory also holds
// the project's own scaffolding (package.json, tsconfig.json, biome.json, the
// lockfile, node_modules) which exists to LINT the templates and is not itself
// something anyone copies into a host, so the list is explicit rather than a
// directory walk that would drag all of it into the guide.
var hookTemplates = []string{
	"magus-command.sh",
	"magus-path.sh",
	// The two templates that carry no verdict: one records a path an agent reached,
	// the other where the work stood when a session stopped, and neither judges
	// anything. So they declare no guard coverage and owe no parity row. See the note
	// at the top of each for why that absence is deliberate rather than a hole.
	"magus-observe.sh",
	"magus-checkpoint.sh",
	// The third of them: it reports where a checkout stands to a session that lost
	// its history, and judges nothing either.
	"magus-rehydrate.sh",
	// The Buzz ports of the three above, which a `magus buzz` wiring names instead of
	// the sh copy. They are shipped artifacts in their own right: a reader downloads
	// one, magus's own config runs them, and they carry their own version marker and
	// coverage declarations. guard_templates.txtar runs both forms of every recorded
	// event and refuses a byte of difference, so what they owe the gates below is the
	// same as what their sh twins owe.
	"magus-command.buzz",
	"magus-path.buzz",
	"magus-observe.buzz",
	// And the Buzz ports of the two verdict-free wrappers, which is what leaves this
	// repository's Claude Code wiring needing neither a POSIX shell nor jq.
	"magus-checkpoint.buzz",
	"magus-rehydrate.buzz",
	"codex-hooks.json",
	"cursor-hook.sh",
	"opencode-plugin.ts",
	// The session-load adapters are shipped artifacts too: version-stamped, embedded
	// in their page, and registered here so a new one cannot arrive unnoticed. They
	// carry no guard coverage, because they judge nothing, and answer the session
	// parity gate below instead.
	"magus-session-load-claude-code.sh",
	"magus-session-load-codex.sh",
	"magus-session-load-opencode.sh",
}

// hookEntry is one wiring in a host's hook config: an optional matcher and the
// commands it runs when the event fires.
type hookEntry struct {
	Matcher string `json:"matcher"`
	Hooks   []struct {
		Command string `json:"command"`
	} `json:"hooks"`
}

// hookSettings is a host hook config keyed by EVENT NAME rather than by the one
// event this file used to model. Every job magus does through a host is a hook on
// some event, and a gate that reads only the pre-tool ones cannot tell a config
// that records no checkpoint and rehydrates no compacted session from one that
// does, which is exactly the parity question these files exist to answer.
type hookSettings struct {
	Hooks map[string][]hookEntry `json:"hooks"`
}

// shippedHookConfigs are the host hook configs this repository owns: the one it
// dogfoods, and the one it ships for a reader to copy. Both are compared against
// each other below, because "whatever magus does on one host it does on every host
// that can express it" is a claim about these two files more than about any prose.
var shippedHookConfigs = map[string]string{
	"claude-code": dogfoodedHookConfig,
	"codex":       hookTemplateDir + "/codex-hooks.json",
}

// hookConfigExemptions records a template one config deliberately does not run,
// with the reason it does not. An exemption is the sanctioned way to differ; the
// unsanctioned way is to differ silently, which is what the gate refuses.
var hookConfigExemptions = map[string]map[string]string{}

// mcpToolMatcherPrefix is how a host config selects magus's own MCP tools. A job
// wired under it guards a different surface from the same template, so the name
// below carries it and the parity gate can see the two apart.
const mcpToolMatcherPrefix = "mcp__magus__"

// shippedGlue reports whether a registered template is one of the executable glue
// files a host config names, in either form: the POSIX sh copies and the Buzz ports.
// It excludes codex-hooks.json, which is a config that NAMES glue rather than glue,
// and opencode-plugin.ts, which a host loads rather than a config naming it.
func shippedGlue(name string) bool {
	switch filepath.Ext(name) {
	case ".sh", ".buzz":
		return true
	default:
		return false
	}
}

// configJobs returns the JOBS a hook config's commands invoke, keyed by the template
// and, where the matcher selects magus's MCP tools, by that surface too.
//
// Keyed by job rather than by file because a template wired twice under different
// matchers is two jobs: claude-code runs magus-command.sh on Bash AND on the
// MCP tool call, and a gate collecting basenames alone reads the second as nothing
// new, which is the whole absence it exists to report.
func configJobs(t *testing.T, path string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "read %s", path)
	var cfg hookSettings
	require.NoError(t, json.Unmarshal(raw, &cfg), "parse %s", path)
	require.NotEmpty(t, cfg.Hooks, "%s wires no events at all", path)

	found := map[string]bool{}
	for _, entries := range cfg.Hooks {
		for _, entry := range entries {
			for _, h := range entry.Hooks {
				for _, template := range hookTemplates {
					if !shippedGlue(template) || !strings.Contains(h.Command, template) {
						continue
					}
					// Keyed by the stem, so a host wiring the Buzz port counts as running
					// the same JOB as a host wiring the sh copy. The two render identical
					// replies, and the gate below asks whether a host does the work, not
					// which of the two files it reached for.
					name := strings.TrimSuffix(strings.TrimSuffix(template, ".sh"), ".buzz")
					if strings.Contains(entry.Matcher, "mcp__") {
						name += " on " + mcpToolMatcherPrefix
					}
					found[name] = true
				}
			}
		}
	}
	return found
}

// TestShippedHookConfigsWireTheSameJobs is the parity gate one level above the
// coverage declarations: those say what a template CAN carry, this says whether a
// host was actually wired to run it.
//
// The failure it prevents is the quiet one. A job added for one host, such as a
// checkpoint or a post-compaction brief, is a one-line addition to that host's
// config, and every other host keeps working, keeps passing, and silently does
// less. Nothing surfaces the difference, because a hook that was never wired
// produces no output to be missing.
func TestShippedHookConfigsWireTheSameJobs(t *testing.T) {
	wired := map[string]map[string]bool{}
	for host, path := range shippedHookConfigs {
		wired[host] = configJobs(t, path)
	}

	for host, templates := range wired {
		for other, otherTemplates := range wired {
			if other == host {
				continue
			}
			for name := range otherTemplates {
				if templates[name] {
					continue
				}
				why, exempt := hookConfigExemptions[host][name]
				assert.True(t, exempt,
					"%s runs %s and %s does not.\n"+
						"Wire it there too, or record why that host does without it in hookConfigExemptions.\n"+
						"A host doing less than another is a decision; a host doing less than another with\n"+
						"nothing saying so is the gap this gate exists to refuse.",
					shippedHookConfigs[other], name, shippedHookConfigs[host])
				if exempt {
					t.Logf("%s does not run %s: %s", host, name, why)
				}
			}
		}
	}

	for host, exemptions := range hookConfigExemptions {
		for name := range exemptions {
			assert.False(t, wired[host][name],
				"hookConfigExemptions says %s does not run %s, but %s invokes it. Drop the exemption.",
				host, name, shippedHookConfigs[host])
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
	"host-e2e.ts":             true,
	"host-e2e.test.ts":        true,
	// Emits testdata/hosts/cursor/gen from @cursor/sdk's published zod. A generator
	// magus runs, not an artifact a reader installs into a host, so it owes the parity
	// gates nothing: no guide embeds it and no host wires it.
	"cursor-schemas.ts": true,
	// The binary-interface twin: proves the recorded shim's argv shape still gets
	// a real verdict from a real magus, but is not itself something a reader
	// copies into a host; see the note at its top for the split with the file
	// above.
	"opencode-plugin.live.test.ts": true,
}

// TestEveryShippedTemplateIsRegistered closes the gate the other parity tests
// leave open: they all iterate hookTemplates, so an artifact missing from that
// list is not merely untested, it is invisible to every check at once: not
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
		case ".sh", ".ts", ".json", ".buzz":
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
// carries. The two artifacts differ in who owns them (a skill is generated and
// regraded on every `magus doctor`, a template is copied into a host's config
// and owned by its reader from then on), so the marker cannot be a content
// digest without flagging every customization the templates explicitly invite.
// A version survives editing and still answers the one question that matters to
// a reader: is my copy older than the fix?
//
// The forcing function only works if the bump reaches every file. Without this,
// bumping the constant for a fix in one template would leave the other three
// claiming a version whose behavior they do not have, which is worse than no
// marker at all: it would be a wrong answer rather than a missing one.
func TestShippedTemplatesCarryTheCurrentVersion(t *testing.T) {
	want := fmt.Sprintf("%s %d", GuardTemplateMarker, GuardTemplateVersion)
	for _, name := range hookTemplates {
		// codex-hooks.json is JSON: no comment syntax to carry a marker, and an
		// invented key risks the host rejecting the config. It ships no logic of
		// its own (it names the two templates that do), so its version is theirs.
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
// legitimate answer (Cursor sends nothing on an allow, so an advise dies there),
// and recording it is the point. Silence is the bug, because silence is
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
				name, fields["schema"], GuardSchemaVersion)
			surface := fields["surface"]
			require.Contains(t, GuardSurfaces(), surface, "%s declares an unknown guard surface %q", name, surface)
			require.NotEmpty(t, fields["host"], "%s: coverage declaration names no host", name)

			for _, host := range strings.Split(fields["host"], ",") {
				if cov[host] == nil {
					cov[host] = map[string]map[string]string{}
				}
				stances := map[string]string{}
				for _, decision := range GuardDecisions() {
					stance, ok := fields[decision]
					require.True(t, ok,
						"%s declares the %s surface for host %q but says nothing about the %q decision.\n"+
							"Every decision in agent.GuardDecisions needs an explicit stance (model, human, or none):\n"+
							"an undeclared decision is one this host was never asked about.", name, surface, host, decision)
					require.True(t, guardStances[stance], "%s: unknown stance %q for %q (want model, human, or none)", name, stance, decision)
					stances[decision] = stance
				}
				// A host may be served on one surface by two artifacts, because a
				// template ships in sh and in Buzz and a host wires whichever suits
				// its machine. What must not differ is what they CLAIM: two artifacts
				// disagreeing about the same cell leaves the gate unable to say which
				// is true, which is the state the check below refuses. Agreement is
				// cheap to state and is what the executed cases already prove.
				if prior := cov[host][surface]; prior != nil {
					require.Equal(t, prior, stances,
						"%s declares the %s surface for host %q differently from the artifact beside it.\n"+
							"Two forms of one template must claim the same stances, or nothing can say which the host gets.",
						name, surface, host)
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
// difference was recorded only in prose (a table in the guide that nothing
// checked), so adding a decision kind or a guard surface could leave a host
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
// passes here and fails nowhere: that is transport parity, which needs the
// templates executed against real events, and it is not this test.
func TestHostGluesCoverTheGuardContract(t *testing.T) {
	cov := parseGuardCoverage(t)

	for host, surfaces := range cov {
		for _, surface := range GuardSurfaces() {
			assert.NotNil(t, surfaces[surface],
				"host %q declares no coverage for the %q guard surface.\n"+
					"Either a template wires it and needs a %s line, or the host cannot and\n"+
					"some template must say so - a surface nobody claims is a coverage hole nobody sees.",
				host, surface, guardCoverageMarker)
		}
	}

	// Codex points at the generic templates. The claim is read off the GUARD
	// declaration rather than off the file's text: a session-load adapter names
	// the same host on a contract this config has nothing to do with, and matching
	// that would demand a hook wiring for a file nobody wires to a hook.
	wiring, err := os.ReadFile(filepath.Join(hookTemplateDir, "codex-hooks.json"))
	require.NoError(t, err)
	for _, name := range hookTemplates {
		body, err := os.ReadFile(filepath.Join(hookTemplateDir, name))
		require.NoError(t, err)
		if !claimsGuardHost(string(body), "codex") {
			continue
		}
		assert.Contains(t, string(wiring), name,
			"%s claims to cover the codex host, but codex-hooks.json never invokes it", name)
	}
}

// claimsGuardHost reports whether a template names host in one of its guard
// coverage declarations.
func claimsGuardHost(body, host string) bool {
	for _, line := range strings.Split(body, "\n") {
		_, decl, found := strings.Cut(line, guardCoverageMarker)
		if !found {
			continue
		}
		for _, kv := range strings.Fields(decl) {
			key, value, ok := strings.Cut(kv, "=")
			if !ok || key != "host" {
				continue
			}
			if slices.Contains(strings.Split(value, ","), host) {
				return true
			}
		}
	}
	return false
}

// failOpenArmRe matches the tests a shipped template makes before answering
// WITHOUT a verdict from magus: the binary is missing or not executable, or it
// ran and left nothing to report. All three spellings the templates use: sh, TS
// and Buzz.
var failOpenArmRe = regexp.MustCompile(`! -x "\$__MAGUS_BIN"|-z "\$verdict"|stdout === null` +
	`|!isExecutable\(bin\)|trimTrailingNewlines\(result!\.stdout\) == ""`)

// failOpenRetryRe marks a block that re-invokes magus rather than answering. Every
// template tests the same empty-verdict condition twice (once to retry without the
// attribution flags, once to give up), and only the second is a fail-open arm.
var failOpenRetryRe = regexp.MustCompile(`\$\(guard\b|runOnce\(|judge\(guard, extra: \[<str>\]\)`)

// failOpenNoticeRe matches an arm SAYING it did not judge the call: prose on
// stderr, a console warning, or one of the __MAGUS_*_RESPONSE envelopes. The Buzz
// ports read that envelope through envOr, so the fallback they hand it is the
// notice, and it is named here rather than reached through a variable. That
// fallback is now a CALL, because the PermissionRequest surface has no context
// field to carry prose and takes the same notice by another route.
var failOpenNoticeRe = regexp.MustCompile(`>&2|console\.warn|unguarded\(\)|\$__MAGUS_[A-Z_]+_RESPONSE` +
	`|text: UNAVAILABLE_TEXT`)

// failOpenOptInRe matches the shape that makes a notice OPT-IN: the arm prints
// only when the reader has set the variable, so by default it prints nothing.
var failOpenOptInRe = regexp.MustCompile(`^\[ -n "\$__MAGUS_[A-Z_]+" \] &&`)

// failOpenDefaultRe extracts the variable an arm's notice comes from, so the
// default assigned to it can be checked for emptiness.
var failOpenDefaultRe = regexp.MustCompile(`\$(__MAGUS_[A-Z_]+_RESPONSE)`)

// failOpenComputedNoticeRe matches an arm that BUILDS its notice from what it observed
// rather than printing a canned string. There is no variable to give a default to, and
// the evidence is the point: which binary went silent, its version, what it printed.
var failOpenComputedNoticeRe = regexp.MustCompile(`(?m)^\s*guard_failure_notice\b|failureNotice\(guard\b`)

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
	"magus-path.sh":   "cmd/magus/testdata/script/guard_templates.txtar pins the silence; __MAGUS_UNAVAILABLE_RESPONSE and __MAGUS_FAILED_RESPONSE are the opt-in",
	"magus-path.buzz": "the same decision as its sh twin, and the same executed case: the archive runs both forms against the same event and refuses a difference",
}

// TestFailOpenArmsAnnounceThemselves is the doctrine's enforcement point: a
// template that cannot judge a call must SAY so.
//
// Silence and a clean session are the same observation. Every other gate here
// checks what a template does with a verdict it received; this one checks the
// case where it received none, which is the case a reader never notices: the
// guard stops enforcing and the transcript looks exactly as it did before.
//
// Structural on purpose: it finds the arms by the conditions the templates test
// and asks each one for an unconditional notice, so rewording a message costs
// nothing and DELETING one fails. A template with no coverage declaration is not
// asked, which is how magus-observe.sh is exempt: it carries no verdict,
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
//
// An arm that COMPUTES its notice satisfies both halves by construction, and is
// checked first: the override variable it consults before falling back is the
// first thing the block mentions, so reading that line as the notice would
// demand a default the arm exists to do without.
func assertFailOpenNotice(t *testing.T, name, doc string, block []string, line int) {
	t.Helper()
	if failOpenComputedNoticeRe.MatchString(strings.Join(block, "\n")) {
		return
	}
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
			"announces itself (see magus-command.sh's __MAGUS_UNAVAILABLE_RESPONSE). Add a notice,\n"+
			"or record the arm in failOpenSilentByDesign with where the decision to stay quiet is written.",
		name, line)
}

// TestCursorGuardAnnouncesAMissingJq runs the template with no jq on PATH, which
// is the one dependency failure the text scan above cannot see.
//
// Every field that template branches on is selected with jq. Without it they all
// come back empty, the shape fallback has nothing left to infer from, and the
// event reaches the default arm: exit 0, no reply, every deny rule off, and
// nothing saying so, which is indistinguishable from a guarded session.
func TestCursorGuardAnnouncesAMissingJq(t *testing.T) {
	script, err := filepath.Abs(filepath.Join(hookTemplateDir, "cursor-hook.sh"))
	require.NoError(t, err)

	// A PATH holding only what the template needs before it reads the event, plus
	// a magus stub so a missing binary cannot be what answers. jq is not in it.
	bin := t.TempDir()
	for _, tool := range []string{"cat", "printf", "mkdir", "cksum", "cut", "find", "grep"} {
		real, err := exec.LookPath(tool)
		require.NoError(t, err, "this test needs %s", tool)
		require.NoError(t, os.Symlink(real, filepath.Join(bin, tool)))
	}
	require.NoError(t, os.WriteFile(filepath.Join(bin, "magus"), []byte("#!/bin/sh\nexit 0\n"), 0o755))

	cmd := exec.Command("sh", script, "--agent-name", "cursor")
	cmd.Dir = t.TempDir()
	cmd.Env = []string{"PATH=" + bin, "TMPDIR=" + t.TempDir()}
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"beforeShellExecution","command":"rm -rf /","cwd":"/ws"}`)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	require.NoError(t, cmd.Run(),
		"the hook must exit 0: a non-zero status reads as a crash and fails open. stderr: %s", stderr.String())

	assert.Contains(t, stderr.String(), "jq is not on PATH",
		"a guard that cannot read the event must say so, or a disarmed guard looks like a clean session")
	assert.Equal(t, `{"permission":"allow"}`, stdout.String(),
		"a gating event needs an explicit reply; an empty one is read as no opinion")
}

// transportCases is the testscript that executes the sh templates against real
// host events. Its cases are labeled so the contract can demand one per cell.
const transportCases = repoRoot + "/cmd/magus/testdata/script/guard_templates.txtar"

// TestTransportCasesCoverTheContract ties the executed cases to the same
// contract the declarations answer to.
//
// Coverage parity asks every glue to DECLARE a stance; this asks that somebody
// actually ran it. Without this, growing the contract would demand new
// declarations (which the sibling test enforces) while the transport cases
// quietly kept testing the old cells, and a declaration nobody executes is the
// exact failure that let a broken plugin ship.
//
// A label is not a case. The gate demands a block that actually EXECUTES a
// template, because the one form of this file that never fails is a cell whose
// label reads true and whose body runs nothing, and an "unreachable" note is a
// claim about the guard that nothing rechecks once the guard grows a rule.
// unreachableCases is the only door out, and it names the cell and the reason.
func TestTransportCasesCoverTheContract(t *testing.T) {
	executed, noted := transportCasesByCell(t)

	for _, surface := range GuardSurfaces() {
		for _, decision := range GuardDecisions() {
			cell := surface + "/" + decision
			if why, allowed := unreachableCases[cell]; allowed {
				assert.True(t, noted[cell],
					"%s executes no %q case and unreachableCases says it cannot (%s), but no `# case: %s unreachable - <why>`\n"+
						"line says so in the file. Record it where the next person looks, or drop the entry.",
					transportCases, cell, why, cell)
				continue
			}
			assert.True(t, executed[cell],
				"%s has no EXECUTED case for %q: a `# case: %s` label with a block that runs a template.\n"+
					"A label alone, or an `unreachable` note, does not satisfy this: a declared stance nobody\n"+
					"ran is the failure that let a broken plugin ship. If these cases genuinely cannot produce\n"+
					"this verdict, add %q to unreachableCases with the reason.",
				transportCases, cell, cell, cell)
		}
	}

	for cell := range unreachableCases {
		assert.False(t, executed[cell],
			"unreachableCases says %s cannot execute %q, but a case for it runs a template. Drop the entry.",
			transportCases, cell)
	}
	for cell := range noted {
		_, allowed := unreachableCases[cell]
		assert.True(t, allowed,
			"%s calls %q unreachable and unreachableCases does not. An unreachable note is a claim about the\n"+
				"guard that nothing rechecks once a rule grows; record it in unreachableCases so this gate owns it,\n"+
				"or delete the note and write the case.", transportCases, cell)
	}
}

// unreachableCases records a surface-and-decision cell the cases cannot execute,
// and why. Every other cell owes a real case.
var unreachableCases = map[string]string{
	"mcp/advise": "every rule that fires on a judged MCP call denies, and the advisory families that " +
		"could reach one (gate-repeat, graph-stale) each need state a testscript cannot " +
		"make deterministic: run logs inside the window, a stale symbol index",
	"path/ask": "the push gate is the only rule that asks, and it reads a shell command; no rule asks about a write. " +
		"The arm is rendered and graded by TestRenderedGuardVerdictsValidateAgainstTheirHostSchema instead",
	"mcp/ask": "the push gate is the only rule that asks, and it reads a shell command; no rule asks about an MCP call. " +
		"The arm is rendered and graded by TestRenderedGuardVerdictsValidateAgainstTheirHostSchema instead",
}

// transportCasesByCell splits the cases on their labels and reports, per cell,
// whether some block for it runs a template and whether some block claims it is
// unreachable. A cell may carry several cases, so both are ORs over its blocks.
func transportCasesByCell(t *testing.T) (executed, noted map[string]bool) {
	t.Helper()
	body, err := os.ReadFile(transportCases)
	require.NoError(t, err, "read %s", transportCases)

	executed, noted = map[string]bool{}, map[string]bool{}
	cell := ""
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if rest, found := strings.CutPrefix(line, "# case: "); found {
			fields := strings.Fields(rest)
			if len(fields) == 0 {
				cell = ""
				continue
			}
			cell = fields[0]
			if len(fields) > 1 && fields[1] == "unreachable" {
				noted[cell] = true
				cell = ""
			}
			continue
		}
		if cell != "" && strings.HasPrefix(line, "exec ") {
			executed[cell] = true
		}
	}
	return executed, noted
}

// TestShellTransportCasesCoverTheHostContract is the host-aware companion to
// TestTransportCasesCoverTheContract. The latter proves every guard cell runs
// somewhere; that is insufficient for a shared template, where a new host can
// claim the same reply dialect without any fixture ever naming it. The `# hosts:`
// markers bind an executed recorded event to each shell host that relies on it.
//
// OpenCode is deliberately outside these cases: it is TypeScript rather than sh,
// and docs/guides/integrations/agents/opencode-plugin.test.ts executes its
// plugin interface directly (with a separate live-binary twin). Keeping its
// runtime-specific test in the project that owns the runtime avoids a fake shell
// adapter merely to make this table uniform.
func TestShellTransportCasesCoverTheHostContract(t *testing.T) {
	executed := transportCasesByHost(t)
	coverage := parseGuardCoverage(t)

	for host := range shellGuardHosts(t) {
		for _, surface := range GuardSurfaces() {
			wired := false
			for _, decision := range GuardDecisions() {
				wired = wired || coverage[host][surface][decision] != "none"
			}
			if !wired {
				continue
			}
			for _, decision := range GuardDecisions() {
				cell := surface + "/" + decision
				if _, unreachable := unreachableCases[cell]; unreachable {
					continue
				}
				assert.True(t, executed[host][cell],
					"%s wires the %s surface, but no `# hosts: %s` fixture executes %s. "+
						"Record the host beside the real event that reaches its adapter, or change the declaration.",
					host, surface, host, cell)
			}
		}
	}
}

// shellGuardHosts discovers the hosts served by a shell template rather than
// duplicating a provider list beside the cases. A new shell-backed host therefore
// owes fixtures as soon as it adds its coverage marker.
func shellGuardHosts(t *testing.T) map[string]bool {
	t.Helper()
	hosts := map[string]bool{}
	for _, name := range hookTemplates {
		if filepath.Ext(name) != ".sh" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(hookTemplateDir, name))
		require.NoError(t, err, "read %s", name)
		for _, match := range coverageHost.FindAllStringSubmatch(string(body), -1) {
			hosts[match[1]] = true
		}
	}
	require.NotEmpty(t, hosts, "no shell template declared a host, so the host transport gate graded nothing")
	return hosts
}

// transportCasesByHost reports the cells an executed fixture reaches for
// each `# hosts:` declaration. A host marker remains active until the next one,
// matching the cases' section structure; an unlabeled section cannot satisfy a
// host claim by accident.
func transportCasesByHost(t *testing.T) map[string]map[string]bool {
	t.Helper()
	body, err := os.ReadFile(transportCases)
	require.NoError(t, err, "read %s", transportCases)

	executed := map[string]map[string]bool{}
	var hosts []string
	cell := ""
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if rest, found := strings.CutPrefix(line, "# hosts: "); found {
			hosts = strings.Split(rest, ",")
			for i := range hosts {
				hosts[i] = strings.TrimSpace(hosts[i])
				require.NotEmpty(t, hosts[i], "%s has an empty host in %q", transportCases, line)
			}
			continue
		}
		if rest, found := strings.CutPrefix(line, "# case: "); found {
			fields := strings.Fields(rest)
			cell = ""
			if len(fields) > 0 && (len(fields) == 1 || fields[1] != "unreachable") {
				cell = fields[0]
			}
			continue
		}
		if cell == "" || !strings.HasPrefix(line, "exec ") {
			continue
		}
		for _, host := range hosts {
			if executed[host] == nil {
				executed[host] = map[string]bool{}
			}
			executed[host][cell] = true
		}
	}
	return executed
}

// crosscheckLib holds the docs checks that compare a page with the files it documents:
// the parity tables, the embedded templates, the host wiring. They run in the docs
// project's conventions target, so no test here reads a page; the tests below hold its
// hand-kept lists to the ones this file owns.
const crosscheckLib = repoRoot + "/docs/lib/crosscheck.buzz"

// buzzListBlock captures the body of `export final <NAME> = [ ... ];` or `{ ... };`.
func buzzListBlock(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(crosscheckLib)
	require.NoError(t, err, "read %s", crosscheckLib)
	m := regexp.MustCompile(`(?s)export final ` + name + ` = [\[{](.*?)[\]}];`).FindStringSubmatch(string(body))
	require.Len(t, m, 2, "%s declares no %s", crosscheckLib, name)
	return m[1]
}

// TestEveryShippedTemplateHasAPage holds the docs check's page map to hookTemplates, so
// a template shipped here owes a page there. Which page carries it verbatim, and
// whether it still does, is the docs conventions target's question.
func TestEveryShippedTemplateHasAPage(t *testing.T) {
	var keyed []string
	for _, m := range regexp.MustCompile(`"([^"]+)":`).FindAllStringSubmatch(buzzListBlock(t, "TEMPLATE_PAGES"), -1) {
		keyed = append(keyed, m[1])
	}
	assert.ElementsMatch(t, hookTemplates, keyed,
		"%s TEMPLATE_PAGES must name a page for exactly the templates in hookTemplates", crosscheckLib)
}

// TestRehydrateTemplateInvokesTheBrief: both forms of the rehydration hook exist to
// print the brief. That the host page names the post-compaction event they run on is
// checked with the page, in the docs conventions target.
func TestRehydrateTemplateInvokesTheBrief(t *testing.T) {
	for _, name := range []string{"magus-rehydrate.sh", "magus-rehydrate.buzz"} {
		body, err := os.ReadFile(filepath.Join(hookTemplateDir, name))
		require.NoError(t, err, "read %s", name)
		assert.Contains(t, string(body), "session --brief",
			"%s must invoke the brief; it has no other reason to exist", name)
	}
}

// hostOutputSchema names the schema a rendered verdict is graded against. Codex's is
// OpenAI's own generated one; Claude Code's and Cursor's are ours, because neither
// publishes a schema for hook stdout. testdata/hosts/SOURCES.md says which is
// which and refuses to let that distinction blur.
var hostOutputSchema = map[string]string{
	"claude-code": "claude-code/hook-output.schema.json",
	"codex":       "codex/pre-tool-use.command.output.schema.json",
	"cursor":      "cursor/hook-output.schema.json",
}

// cursorReplySchema maps a shell template variable in cursor-hook.sh to the schema for
// the event that reads what it renders.
//
// One entry per host is enough everywhere else. Cursor validates each event's stdout
// with a different function, and its gating events carry no advisory channel, so the
// guard answers two events with two shapes; a single schema for the host would have to
// be the union of both, which validates each reply against fields the event reading it
// ignores.
var cursorReplySchema = map[string]string{
	"gate":   "cursor/hook-output.schema.json",
	"advise": "cursor/post-tool-use.output.schema.json",
}

// coverageHost reads the host out of a template's `magus-guard-coverage:` marker. The
// marker already carries the host-parity contract, so deriving from it rather than from
// a second list here means a template that learns a new host demands that host's schema
// on the next run instead of quietly going ungraded.
var coverageHost = regexp.MustCompile(`magus-guard-coverage:.*\bhost=(\S+)`)

// The literal JSON a template prints without asking magus at all, and the shell variables
// cursor-hook.sh keeps its reply templates in.
var (
	literalJSONObject = regexp.MustCompile(`'(\{"[^'\n]*\})'`)
	shellTemplateVar  = regexp.MustCompile(`(?m)^(\w+)_template='(.*)'$`)
)

// permissionRequestEvent is the hookEventName of Codex's approval-request reply, which is
// graded against its own schema rather than PreToolUse's.
const (
	permissionRequestEvent  = `"hookEventName":"PermissionRequest"`
	permissionRequestSchema = "codex/permission-request.command.output.schema.json"
)

// guardVerdicts are the decisions `magus shell` renders, plus one it never does. The values
// are fixed here rather than taken from a run so the rendered bytes belong to the test, and
// the reason carries the characters a naive template would break on. The unknown decision
// stands in for a contract that grew after a copy was installed: it must never render as
// an allow.
var guardVerdicts = []map[string]any{
	{"decision": "deny", "reason": `git stash is denied here: "quoted" & <angled>`},
	{"decision": "advise", "context": "magus workspace: `magus refs <sym>` for code"},
	{"decision": "pass"},
	{"decision": "ask", "reason": `pushing abc1234, which no passing gate covers: "quoted" & <angled>`},
	{"decision": "maybe", "reason": "a decision no template knows"},
}

// templateArrangement is one way a host reaches a shared sh template: the environment its
// wiring sets, the event it sends, and whether a Codex prompt rule sits in the project.
type templateArrangement struct {
	label string
	// host is the name the wiring passes as --agent-name, the only place a template reads
	// one. Every arrangement names one: a template given none never assembles a reply for
	// magus to render, because magus refuses it (MGS3024), which the transport cases pin.
	host  string
	env   []string
	event string
	rules bool
	// codex marks an arrangement whose PreToolUse reply must never carry permissionDecision
	// "ask": Codex parses it, reports the hook failed, and runs the call anyway.
	codex bool
	// ownResponse is a reader-written HOST_RESPONSE, which cannot claim --renders-ask.
	ownResponse bool
	// wantsAsk marks an arrangement whose ask must render as the host's own prompt.
	wantsAsk bool
}

var templateArrangements = []templateArrangement{
	{label: "claude-code", host: "claude-code", event: `{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "claude-code without the advise arm", host: "claude-code", env: []string{"__MAGUS_NO_ADVISE=1"}, event: `{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "codex with no prompt rule", host: "codex", codex: true, event: `{"hook_event_name":"PreToolUse","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "codex with its prompt rule", host: "codex", codex: true, rules: true, event: `{"hook_event_name":"PreToolUse","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "codex in a mode that never prompts", host: "codex", codex: true, rules: true, event: `{"hook_event_name":"PreToolUse","session_id":"s","permission_mode":"bypassPermissions","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "codex approval request for a push", host: "codex", codex: true, rules: true, event: `{"hook_event_name":"PermissionRequest","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"git push origin HEAD"}}`},
	{label: "codex approval request for anything else", host: "codex", codex: true, rules: true, event: `{"hook_event_name":"PermissionRequest","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"rm -rf build"}}`},
	// The payload never overrides the name: an event carrying Codex's turn_id, under a
	// wiring that names claude-code, gets Claude Code's ask. Checked by wantsAsk below.
	{label: "claude-code named, Codex-shaped event", host: "claude-code", wantsAsk: true, event: `{"hook_event_name":"PreToolUse","session_id":"s","turn_id":"t","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "a reader's own HOST_RESPONSE", host: "claude-code", ownResponse: true, env: []string{`HOST_RESPONSE={{if eq .decision "deny"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson .reason}}}}{{end}}`}, event: `{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
}

// templateEcho stands in for magus: it prints the template it was handed and nothing else,
// so what a script ASSEMBLES for a host is read from the script running rather than from
// a regex over its source. It records whether the call claimed --renders-ask in the file
// $RENDERS_ASK_RECORD names.
const templateEcho = "#!/bin/sh\nclaim=no\nfor a in \"$@\"; do [ \"$a\" = --renders-ask ] && claim=yes; done\nprintf '%s' \"$claim\" > \"$RENDERS_ASK_RECORD\"\n" +
	"while [ $# -gt 0 ]; do\n  if [ \"$1\" = -o ]; then printf '%s' \"${2#template=}\"; exit 0; fi\n  shift\ndone\nexit 1\n"

// renderedTemplate is one template body a script assembled, the arrangement it came from,
// and whether the call claimed --renders-ask.
type renderedTemplate struct {
	arrangement templateArrangement
	body        string
	rendersAsk  bool
}

// assembledTemplates runs a shipped sh template once per arrangement against templateEcho
// and returns each template body it would hand magus.
func assembledTemplates(t *testing.T, path string) []renderedTemplate {
	t.Helper()
	for _, tool := range []string{"sh", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("the shared templates run under sh and read the event with jq; %s is not installed", tool)
		}
	}
	script, err := filepath.Abs(path)
	require.NoError(t, err)
	bin := filepath.Join(t.TempDir(), "magus")
	require.NoError(t, os.WriteFile(bin, []byte(templateEcho), 0o755))

	var out []renderedTemplate
	for _, a := range templateArrangements {
		dir := t.TempDir()
		if a.rules {
			rules := filepath.Join(dir, ".codex", "rules", "magus.rules")
			require.NoError(t, os.MkdirAll(filepath.Dir(rules), 0o755))
			require.NoError(t, os.WriteFile(rules, []byte("prefix_rule(pattern = [\"git\", \"push\"], decision = \"prompt\")\n"), 0o644))
		}
		record := filepath.Join(dir, "renders-ask")
		cmd := exec.Command("sh", script, "--agent-name", a.host)
		cmd.Dir = dir
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "TMPDIR=" + t.TempDir(), "__MAGUS_BIN=" + bin, "RENDERS_ASK_RECORD=" + record}, a.env...)
		cmd.Stdin = strings.NewReader(a.event)
		body, err := cmd.Output()
		require.NoError(t, err, "%s (%s)", path, a.label)
		require.NotEmpty(t, body, "%s (%s) handed magus no template", path, a.label)
		claim, err := os.ReadFile(record)
		require.NoError(t, err, "%s (%s) never called magus", path, a.label)
		out = append(out, renderedTemplate{arrangement: a, body: string(body), rendersAsk: string(claim) == "yes"})
	}
	return out
}

// guardResponses returns every JSON document a shipped sh template can print: the literal
// ones, and the renderings of the Go template it assembles for each arrangement.
func guardResponses(t *testing.T, path, body string) []string {
	t.Helper()
	name := filepath.Base(path)
	var out []string
	for _, match := range literalJSONObject.FindAllStringSubmatch(body, -1) {
		out = append(out, match[1])
	}
	// Only the shared templates assemble HOST_RESPONSE; cursor-hook.sh keeps its replies in
	// variables TestCursorGuardRepliesValidateAgainstTheEventThatReadsThem grades.
	if !strings.Contains(body, "HOST_RESPONSE") {
		return out
	}
	funcs := sprig.HermeticTxtFuncMap()
	for _, assembled := range assembledTemplates(t, path) {
		a := assembled.arrangement
		// The claim is what lets magus return an ask at all, so it must track exactly the
		// replies that render one: every reply the template assembles, and none a reader wrote.
		assert.Equal(t, !a.ownResponse, assembled.rendersAsk,
			"%s (%s): --renders-ask claimed=%v; a template's own reply must claim it and a reader's must not", name, a.label, assembled.rendersAsk)
		tmpl, err := template.New(name).Funcs(funcs).Parse(assembled.body)
		require.NoError(t, err, "%s (%s) renders its verdict through this template, so it must parse", name, a.label)
		for _, verdict := range guardVerdicts {
			var rendered strings.Builder
			require.NoError(t, tmpl.Execute(&rendered, verdict), "%s (%s) on a %s", name, a.label, verdict["decision"])
			text := rendered.String()
			// A reader's own reply is theirs to get right; magus sends it no ask.
			decision := verdict["decision"]
			if a.ownResponse {
				decision = ""
			}
			switch decision {
			case "maybe":
				assert.Contains(t, text, "deny", "%s (%s) must refuse a decision it does not know, never allow it", name, a.label)
			case "ask":
				assert.NotEmpty(t, text, "%s (%s) renders an ask as nothing, which the host takes as allow", name, a.label)
				if a.codex {
					assert.NotContains(t, text, `"permissionDecision":"ask"`,
						"%s (%s): Codex parses a hook ask, marks the hook failed, and runs the call", name, a.label)
				}
				if a.wantsAsk {
					assert.Contains(t, text, `"permissionDecision":"ask"`,
						"%s (%s): the wiring's host decides the arm, never the event's shape", name, a.label)
				}
			}
			if strings.HasPrefix(text, "{") {
				out = append(out, text)
			}
		}
	}
	return out
}

// TestRenderedGuardVerdictsValidateAgainstTheirHostSchema grades the bytes a host
// actually receives.
//
// Rendered from the template bodies in the shipped files rather than from a copy pasted
// here, so an edit to a template is graded on the next run instead of drifting away from
// a fixture nobody updates.
func TestRenderedGuardVerdictsValidateAgainstTheirHostSchema(t *testing.T) {
	templates, err := filepath.Glob(filepath.Join(hookTemplateDir, "*.sh"))
	require.NoError(t, err)
	require.NotEmpty(t, templates, "the shipped templates must be discoverable")

	var graded int
	for _, path := range templates {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		body := string(raw)

		hosts := map[string]bool{}
		for _, match := range coverageHost.FindAllStringSubmatch(body, -1) {
			hosts[match[1]] = true
		}
		// No coverage marker means the template carries no verdict at all. It observes,
		// or checkpoints, or rehydrates, and has no host reply to grade. That absence is
		// deliberate and the coverage gates above already hold it to it.
		if len(hosts) == 0 {
			continue
		}
		graded++

		t.Run(filepath.Base(path), func(t *testing.T) {
			responses := guardResponses(t, path, body)
			require.NotEmpty(t, responses,
				"%s prints no JSON at all, which means the extraction above stopped matching it\n"+
					"rather than that the template stopped answering", path)

			// A reply to Codex's approval request answers a different event, with its own
			// published schema, and no other host is wired to raise it.
			approvals := loadHostSchema(t, permissionRequestSchema)
			for _, response := range responses {
				if !strings.Contains(response, permissionRequestEvent) {
					continue
				}
				require.True(t, hosts["codex"], "%s answers a PermissionRequest but declares no codex coverage", path)
				assert.NoError(t, approvals.Validate(decodeJSON(t, path, response)),
					"%s prints %s, which codex would not accept, per %s", path, response, permissionRequestSchema)
			}
			for host := range hosts {
				schemaFile, ok := hostOutputSchema[host]
				require.True(t, ok,
					"%s answers %s and no schema grades that host's stdout; vendor one under %s\n"+
						"and record it in SOURCES.md", path, host, hostSchemaDir)
				schema := loadHostSchema(t, schemaFile)
				for _, response := range responses {
					if strings.Contains(response, permissionRequestEvent) {
						continue
					}
					assert.NoError(t, schema.Validate(decodeJSON(t, path, response)),
						"%s prints %s, which %s would not accept, per %s", path, response, host, schemaFile)
				}
			}
		})
	}
	assert.Positive(t, graded,
		"no template declared a magus-guard-coverage host, so this gate graded nothing and said so\n"+
			"by passing")
}

// TestHookOutputSchemasRejectAWrongEventName is the negative for the output direction.
//
// hookEventName is the field a host keys its whole reading of the reply on, so a typo
// there costs the verdict silently. Both output schemas pin it to a constant, and this
// is what proves they still do.
func TestHookOutputSchemasRejectAWrongEventName(t *testing.T) {
	const wrong = `{"hookSpecificOutput":{"hookEventName":"PreToolUseTypo",` +
		`"permissionDecision":"deny","permissionDecisionReason":"nope"}}`

	for _, host := range []string{"claude-code", "codex"} {
		t.Run(host, func(t *testing.T) {
			schema := loadHostSchema(t, hostOutputSchema[host])
			assert.Error(t, schema.Validate(decodeJSON(t, host, wrong)),
				"%s's output schema accepted a misspelled hookEventName", host)
		})
	}

	t.Run("cursor", func(t *testing.T) {
		schema := loadHostSchema(t, hostOutputSchema["cursor"])
		assert.Error(t, schema.Validate(decodeJSON(t, "cursor", `{"permission":"denied"}`)),
			"cursor's output schema accepted a permission value Cursor does not define")

		advise := loadHostSchema(t, cursorReplySchema["advise"])
		assert.Error(t, advise.Validate(decodeJSON(t, "cursor", `{"additional_context":{"text":"nope"}}`)),
			"cursor's postToolUse schema accepted an advisory that is not a string, which Cursor drops")
	})
}

// schemaPropertyNames reads the field names a vendored schema declares.
//
// Read out of the raw JSON rather than off the resolved schema because the question is
// what the host NAMES, and jsonschema-go is built to validate rather than to introspect.
func schemaPropertyNames(t *testing.T, rel string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(hostSchemaDir, rel))
	require.NoError(t, err, "read the vendored schema %s", rel)
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &schema))
	require.NotEmpty(t, schema.Properties, "%s must name the fields it grades", rel)
	return schema.Properties
}

// TestCursorGuardRepliesValidateAgainstTheEventThatReadsThem grades what cursor-hook.sh
// answers, per event.
//
// The generic extraction above cannot reach these. The guard holds its two replies in
// shell variables and splices them in as `template=$gate_template`, so the only Cursor
// bytes that gate ever saw were the two literal allow replies, leaving the deny path
// ungraded, which is the one path a guard exists for.
//
// Both halves of the check matter and only one of them is a schema. Validating catches a
// field whose TYPE moved. The subset assertion catches a field Cursor RENAMED, which
// validating cannot: Cursor's stdout validators read the fields they know and ignore the
// rest, so a reply naming additional_context_v2 is accepted, ignored, and carries no
// advisory at all.
func TestCursorGuardRepliesValidateAgainstTheEventThatReadsThem(t *testing.T) {
	path := filepath.Join(hookTemplateDir, "cursor-hook.sh")
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "read %s", path)

	templates := shellTemplateVar.FindAllStringSubmatch(string(raw), -1)
	require.Len(t, templates, len(cursorReplySchema),
		"%s must render one reply template per mapped Cursor event; a template that stopped\n"+
			"being assigned on its own line reads here as one that stopped existing", path)

	funcs := sprig.HermeticTxtFuncMap()
	for _, match := range templates {
		name := match[1]
		t.Run(name, func(t *testing.T) {
			schemaFile, ok := cursorReplySchema[name]
			require.True(t, ok,
				"%s renders %s_template and nothing says which Cursor event reads it", path, name)
			schema := loadHostSchema(t, schemaFile)
			named := schemaPropertyNames(t, schemaFile)

			tmpl, err := template.New(name).Funcs(funcs).Parse(match[2])
			require.NoError(t, err, "%s_template carries the reply, so it must parse", name)
			for _, verdict := range guardVerdicts {
				var out strings.Builder
				require.NoError(t, tmpl.Execute(&out, verdict), "%s_template on a %s", name, verdict["decision"])
				reply, isObject := decodeJSON(t, path, out.String()).(map[string]any)
				require.True(t, isObject, "%s_template must render a JSON object", name)
				if name == "gate" {
					switch verdict["decision"] {
					case "ask":
						assert.Equal(t, "ask", reply["permission"], "an ask is Cursor's own approval prompt")
					case "pass", "advise":
						assert.Equal(t, "allow", reply["permission"])
					default:
						assert.Equal(t, "deny", reply["permission"], "only pass and advise may allow; %s must not", verdict["decision"])
					}
				}
				assert.NoError(t, schema.Validate(reply),
					"%s_template renders %s, which Cursor would not accept, per %s", name, out.String(), schemaFile)
				for field := range reply {
					assert.Contains(t, named, field,
						"%s_template names %q, which %s does not. Cursor ignores a field it has never\n"+
							"heard of rather than rejecting it, so a rename costs the verdict in silence.",
						name, field, schemaFile)
				}
			}
		})
	}
}
