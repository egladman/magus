package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

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
	"magus-command.buzz",
	"magus-path.buzz",
	// The templates that carry no verdict: one records a path an agent reached, one
	// where the work stood when a session stopped, and one reports where a checkout
	// stands to a session that lost its history. None judges anything, so they declare
	// no guard coverage and owe no parity row. See the note at the top of each for why
	// that absence is deliberate rather than a hole.
	"magus-observe.buzz",
	"magus-checkpoint.buzz",
	"magus-rehydrate.buzz",
	"magus-session.buzz",
	"codex-hooks.json",
	"cursor-hook.buzz",
	"opencode-plugin.ts",
	// The session-load adapters are shipped artifacts too: version-stamped, embedded
	// in their page, and registered here so a new one cannot arrive unnoticed. They
	// carry no guard coverage, because they judge nothing, and answer the session
	// parity gate below instead.
	"magus-session-load-claude-code.buzz",
	"magus-session-load-codex.buzz",
	"magus-session-load-opencode.buzz",
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
var hookConfigExemptions = map[string]map[string]string{
	"codex": {
		"magus-session": "SessionStart hands a codex hook no env file and reads back only additionalContext " +
			"(testdata/hosts/codex/session-start.command.{input,output}.schema.json), so no hook can put the " +
			"checkout root on PATH for later shell commands",
	},
}

// mcpToolMatcherPrefix is how a host config selects magus's own MCP tools. A job
// wired under it guards a different surface from the same template, so the name
// below carries it and the parity gate can see the two apart.
const mcpToolMatcherPrefix = "mcp__magus__"

// configJobs returns the JOBS a hook config's commands invoke, keyed by the template
// and, where the matcher selects magus's MCP tools, by that surface too.
//
// Keyed by job rather than by file because a template wired twice under different
// matchers is two jobs: claude-code runs magus-command.buzz on Bash AND on the
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
					// Only the Buzz glue is named by a config: codex-hooks.json is a config
					// itself, and a host loads opencode-plugin.ts rather than naming it.
					if filepath.Ext(template) != ".buzz" || !strings.Contains(h.Command, template) {
						continue
					}
					name := strings.TrimSuffix(template, ".buzz")
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
// ran and left nothing to report. Both spellings the templates use: TS and Buzz.
var failOpenArmRe = regexp.MustCompile(`stdout === null` +
	`|!(?:\w+\\)?isExecutable\(bin\)|(?:\w+\\)?trimTrailingNewlines\(result!\.stdout\) == ""`)

// failOpenRetryRe marks a block that re-invokes magus rather than answering. A template
// tests the same empty-verdict condition once per fallback (dropping a capability flag
// an older magus rejects, then the attribution flags) and once more to give up; only the
// last is a fail-open arm.
var failOpenRetryRe = regexp.MustCompile(`runOnce\(|judge\(guard, extra: `)

// failOpenNoticeRe matches an arm SAYING it did not judge the call: a console
// warning, or the fallback a Buzz template hands envOr for one of the
// __MAGUS_*_RESPONSE envelopes. That fallback is a CALL, because the
// PermissionRequest surface has no context field to carry prose and takes the same
// notice by another route.
var failOpenNoticeRe = regexp.MustCompile(`console\.warn|unguarded\(\)|text: UNAVAILABLE_TEXT`)

// failOpenComputedNoticeRe matches an arm that BUILDS its notice from what it observed
// rather than printing a canned string. The evidence is the point: which binary went
// silent, its version, what it printed.
var failOpenComputedNoticeRe = regexp.MustCompile(`failureNotice\(guard\b`)

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
	"magus-path.buzz": "cmd/magus/testdata/script/guard_templates.txtar pins the silence; __MAGUS_UNAVAILABLE_RESPONSE and __MAGUS_FAILED_RESPONSE are the opt-in",
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
// asked, which is how magus-observe.buzz is exempt: it carries no verdict,
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
			assertFailOpenNotice(t, name, block, i+1)
		}
		assert.NotZero(t, arms,
			"%s carries a verdict but no fail-open arm was recognized in it.\n"+
				"Either it now answers some other way - update failOpenArmRe - or it blocks when magus\n"+
				"is unavailable, which is a change this gate should have been told about.", name)
	}
}

// failOpenArmEnd returns the line index just past the block opened at start: the
// next bare `}` at any indent. Adequate for these templates, which never nest a
// block inside a fail-open arm.
func failOpenArmEnd(lines []string, start int) int {
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "}" {
			return i + 1
		}
	}
	return len(lines)
}

// assertFailOpenNotice requires one arm to emit a notice by default: a canned one,
// or one it computes from the evidence it observed. A notice that only an opt-in
// variable carries is silent, and reads like an announcement.
func assertFailOpenNotice(t *testing.T, name string, block []string, line int) {
	t.Helper()
	joined := strings.Join(block, "\n")
	if failOpenComputedNoticeRe.MatchString(joined) || failOpenNoticeRe.MatchString(joined) {
		return
	}
	assert.Fail(t, "fail-open arm says nothing",
		"%s answers without a magus verdict at line %d and emits no default notice.\n"+
			"A guard that stopped enforcing looks exactly like a clean session, so every fail-open arm\n"+
			"announces itself (see magus-command.buzz's UNAVAILABLE_TEXT). Add a notice,\n"+
			"or record the arm in failOpenSilentByDesign with where the decision to stay quiet is written.",
		name, line)
}

// transportCases is the testscript that executes the templates against real
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

// TestTransportCasesCoverTheHostContract is the host-aware companion to
// TestTransportCasesCoverTheContract. The latter proves every guard cell runs
// somewhere; that is insufficient for a shared template, where a new host can
// claim the same reply dialect without any fixture ever naming it. The `# hosts:`
// markers bind an executed recorded event to each Buzz-served host that relies on it.
//
// OpenCode is deliberately outside these cases: it is TypeScript rather than Buzz,
// and docs/guides/integrations/agents/opencode-plugin.test.ts executes its
// plugin interface directly (with a separate live-binary twin). Keeping its
// runtime-specific test in the project that owns the runtime avoids a fake
// adapter merely to make this table uniform.
func TestTransportCasesCoverTheHostContract(t *testing.T) {
	executed := transportCasesByHost(t)
	coverage := parseGuardCoverage(t)

	for host := range buzzGuardHosts(t) {
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

// buzzGuardHosts discovers the hosts served by a Buzz template rather than
// duplicating a provider list beside the cases. A new Buzz-backed host therefore
// owes fixtures as soon as it adds its coverage marker.
func buzzGuardHosts(t *testing.T) map[string]bool {
	t.Helper()
	hosts := map[string]bool{}
	for _, name := range hookTemplates {
		if filepath.Ext(name) != ".buzz" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(hookTemplateDir, name))
		require.NoError(t, err, "read %s", name)
		for _, match := range coverageHost.FindAllStringSubmatch(string(body), -1) {
			hosts[match[1]] = true
		}
	}
	require.NotEmpty(t, hosts, "no Buzz template declared a host, so the host transport gate graded nothing")
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

// TestRehydrateTemplateInvokesTheBrief: the rehydration hook exists to print the brief.
// That the host page names the post-compaction event it runs on is checked with the
// page, in the docs conventions target.
func TestRehydrateTemplateInvokesTheBrief(t *testing.T) {
	const name = "magus-rehydrate.buzz"
	body, err := os.ReadFile(filepath.Join(hookTemplateDir, name))
	require.NoError(t, err, "read %s", name)
	assert.Contains(t, string(body), `"session", "--brief"`,
		"%s must invoke the brief; it has no other reason to exist", name)
}

// hostOutputSchema names the schema a host's hook stdout is graded against. Codex's is
// OpenAI's own generated one; Claude Code's and Cursor's are ours, because neither
// publishes a schema for hook stdout. testdata/hosts/SOURCES.md says which is
// which and refuses to let that distinction blur. The glue's rendered replies are
// graded against these in cmd/magus/buzz_test.go, which can run the Buzz glue.
var hostOutputSchema = map[string]string{
	"claude-code": "claude-code/hook-output.schema.json",
	"codex":       "codex/pre-tool-use.command.output.schema.json",
	"cursor":      "cursor/hook-output.schema.json",
}

// cursorAdviseSchema grades Cursor's postToolUse stdout, the one event that carries an
// advisory. Cursor validates each event's stdout with a different function.
const cursorAdviseSchema = "cursor/post-tool-use.output.schema.json"

// coverageHost reads the host out of a template's `magus-guard-coverage:` marker. The
// marker already carries the host-parity contract, so deriving from it rather than from
// a second list here means a template that learns a new host is held to that host's
// cases on the next run instead of quietly going ungraded.
var coverageHost = regexp.MustCompile(`magus-guard-coverage:.*\bhost=(\S+)`)

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

		advise := loadHostSchema(t, cursorAdviseSchema)
		assert.Error(t, advise.Validate(decodeJSON(t, "cursor", `{"additional_context":{"text":"nope"}}`)),
			"cursor's postToolUse schema accepted an advisory that is not a string, which Cursor drops")
	})
}
