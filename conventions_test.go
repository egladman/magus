package magus

// Source-level convention guards: assertions about the SHAPE of this repository
// rather than the behavior of any symbol in it. They scan the tree as text because
// what they prevent has no runtime signal to observe. The scanners they share live in
// conventions.go.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sync/errgroup"

	"github.com/egladman/magus/internal/config"
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

var scanLoopSkipDirs = skipDirs("worktrees", "gen")

// buzzScanOptOut lets a genuine case through, and demands a reason in the same
// breath: the same shape as the repo's other acknowledged suppressions.
const buzzScanOptOut = "buzz-scan-ok:"

// TestNoRescanningStringLoops keeps a quadratic, memory-retaining idiom out of
// the Buzz sources.
//
// `while (s.indexOf(x) != null) { s = s.replace(x, y) }` is the natural way to
// write replace-all in Buzz, because str.replace substitutes only the FIRST
// occurrence. It is also the most expensive way: each pass copies the whole
// string, and on the default VM build every copy is a distinct string interned
// for the life of the process and never freed (see libs/gopherbuzz/vm/value.go;
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
			if scanLoopSkipDirs[d.Name()] {
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

// Dogfooding checks: assertions that this repository actually uses what it publishes.
//
// Their subject is not a Go symbol but the agreement between three artifacts (the repo's
// own config, the guide, and the templates a reader downloads), so they sit with the other
// conventions rather than beside any one of them. Anything else asserting "we use what we
// ship" belongs here too.
//
// The guard hook this repository dogfoods, the one its documentation teaches,
// and the one a reader downloads must all be the SAME file. They drifted once
// already: the docs kept advertising `command -v magus || exit 0` after the
// shipped config had moved on, so a reader copying the documented form got a
// guard that fails open in silence: the exact failure the change removed.
//
// The templates under docs/guides/integrations/agents/ are the source of truth. This
// repository's own config invokes them rather than inlining a copy, so
// dogfooding exercises the artifact readers actually get. These tests keep that
// true: a config that stops referencing a template is a test failure, and a
// template that stops being embedded in the guide fails the docs conventions
// target, rather than either being something only a careful reader would notice.

const (
	dogfoodedHookConfig = ".claude/settings.json"
	hookTemplateDir     = "docs/guides/integrations/agents"
)

// hookEntry is one wiring in a host's hook config: an optional matcher and the
// commands it runs when the event fires.
type hookEntry struct {
	Matcher string `json:"matcher"`
	Hooks   []struct {
		Command string `json:"command"`
	} `json:"hooks"`
}

// hookSettings is a host hook config keyed by event name.
type hookSettings struct {
	Hooks map[string][]hookEntry `json:"hooks"`
}

// TestDogfoodedHookInvokesTheTemplate keeps this repository honest: its own
// guard must run the same file a reader downloads, not a copy that can drift.
//
// The config is TRACKED, so its absence is a failure rather than a skip. It used to be
// per-developer, on the theory that committing one machine's settings.json would ship that
// machine's wiring everywhere: true of a config naming an absolute path, false of this
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
	require.NotEmpty(t, cfg.Hooks["PreToolUse"], "%s declares no PreToolUse hooks", dogfoodedHookConfig)

	// The one hook that runs with no magus, the session PATH fix, cannot invoke a template,
	// because only magus resolves an embedded one. It is exempt here and held instead to the
	// spell by the harness-drift target, which plans this file with the spell and fails on a
	// missing or stale copy.
	inlined := 0

	// Every event, not only the pre-tool ones. A checkpoint or a rehydration hook
	// inlining its own copy of a template drifts from the file a reader downloads
	// exactly as a guard hook would, and used to do so unwatched.
	for event, entries := range cfg.Hooks {
		for _, entry := range entries {
			require.NotEmpty(t, entry.Hooks, "%s matcher %q has no hooks", event, entry.Matcher)
			for _, h := range entry.Hooks {
				if event == "SessionStart" && !strings.Contains(h.Command, hookTemplateDir) && !strings.Contains(h.Command, "buzz -s ") {
					inlined++
					assert.True(t, strings.HasSuffix(h.Command, " "+types.HarnessOwnedMarker),
						"the inlined %s %q hook must end with the ownership marker, or a merge takes it for a hook of the person's own", event, entry.Matcher)
					continue
				}
				assert.Contains(t, h.Command, hookTemplateDir,
					"the %s %q hook must invoke a template under %s rather than inline its own copy, "+
						"so dogfooding exercises the file readers download", event, entry.Matcher, hookTemplateDir)

				// The referenced file must exist: a hook pointing at a moved or
				// renamed template fails open silently, which is the failure mode
				// this whole arrangement exists to remove.
				for _, field := range strings.Fields(h.Command) {
					if strings.HasPrefix(field, hookTemplateDir) {
						assert.FileExists(t, field, "%s %q hook references a template that does not exist", event, entry.Matcher)
					}
				}
			}
		}
	}
	assert.LessOrEqual(t, inlined, 1, "only the session PATH hook may inline its command")
}

// crosscheckLib holds the docs checks that compare a page with the files it documents:
// the parity tables, the embedded templates, the host wiring. They run in the docs
// project's conventions target, so no test here reads a page; the tests below hold its
// hand-kept lists to the ones this file owns.
const crosscheckLib = "docs/lib/crosscheck.buzz"

// buzzListBlock captures the body of `export final <NAME> = [ ... ];` or `{ ... };`.
func buzzListBlock(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(crosscheckLib)
	require.NoError(t, err, "read %s", crosscheckLib)
	m := regexp.MustCompile(`(?s)export final ` + name + ` = [\[{](.*?)[\]}];`).FindStringSubmatch(string(body))
	require.Len(t, m, 2, "%s declares no %s", crosscheckLib, name)
	return m[1]
}

// TestDocsCheckReadsTheSameDetectionVariables keeps the docs half of the
// environment-sniffing rule reading the variables this file's half reads.
func TestDocsCheckReadsTheSameDetectionVariables(t *testing.T) {
	var listed []string
	for _, m := range quotedString.FindAllStringSubmatch(buzzListBlock(t, "ENVIRONMENT_DETECTION_VARS"), -1) {
		listed = append(listed, m[1])
	}
	assert.Equal(t, environmentDetectionVars, listed,
		"%s ENVIRONMENT_DETECTION_VARS must match environmentDetectionVars", crosscheckLib)
}

var quotedString = regexp.MustCompile(`"([^"]+)"`)

// environmentDetectionVars are the variables that say WHERE magus runs: which CI system,
// which runner, which hosting platform or agent host. Reading one to change WHAT magus does
// (a default, a result, which provider or feature is on) is sniffing; docs/doctrine.md,
// "Told, never guessed", is the rule.
//
// Terminal and interaction variables (TERM, TERM_SESSION_ID, WINDOWID, SSH_TTY and the like)
// are deliberately absent. They describe the terminal in front of the person, and adapting
// how magus talks to that terminal (color, links, hover, which prompt it can show, which
// window a once-only notice belongs to) is the interaction the doctrine keeps. A variable a
// person sets to state what they want (NO_COLOR, MAGUS_*) is configuration, not detection.
var environmentDetectionVars = []string{
	"CI", "GITHUB_ACTIONS", "GITHUB_WORKFLOW_REF", "RUNNER_ENVIRONMENT", "RUNNER_OS",
	"GITLAB_CI", "BUILDKITE", "CIRCLECI", "TRAVIS", "JENKINS_URL", "JENKINS_HOME",
	"TEAMCITY_VERSION", "TF_BUILD", "BITBUCKET_BUILD_NUMBER", "CODEBUILD_BUILD_ID", "DRONE",
	"APPVEYOR", "KUBERNETES_SERVICE_HOST", "container",
	"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CURSOR_TRACE_ID", "CODEX_SANDBOX",
}

// environmentDetectionAllowed maps "<path>:<variable>" to why that read is not sniffing.
// Each entry reads a listed variable as INPUT, after the caller already chose the code that
// reads it, and switches nothing on it.
var environmentDetectionAllowed = map[string]string{
	"spells/github/actions/spell.buzz:GITHUB_WORKFLOW_REF": "input, not detection: the Actions provider, wired only when the workflow asks, reads which workflow's runs last_green_run should search",
}

// skipDirs is the set of directory names a walk in this file skips: version control, cache
// and installed agent state and dependencies, which no convention governs, plus extra.
// .magus changes on every magus run, so a walk that entered it would cost this package
// Go's test cache every time.
func skipDirs(extra ...string) map[string]bool {
	out := map[string]bool{}
	for _, name := range slices.Concat([]string{".git", ".magus", ".claude", ".agents", ".opencode", ".testcache", "node_modules"}, extra) {
		out[name] = true
	}
	return out
}

// environmentDetectionSkipDirs are trees the rule does not govern: generated output,
// vendored code, fixtures, and history. docs is scanned, because the agent glue a reader
// installs lives there; its pages are checked by the docs project instead.
var environmentDetectionSkipDirs = skipDirs("gen", "testdata", "blog", "releases", "manpage", "schema")

var (
	environmentDetectionNames = strings.Join(environmentDetectionVars, "|")
	// A call whose callee spells env or lookup (os.Getenv, os.LookupEnv, os\env, env\get,
	// a local getenv or envOr) with a listed name as its first argument.
	environmentDetectionCall = regexp.MustCompile(
		`(?i:env|lookup)[\w\\]*\s*\(\s*"(` + environmentDetectionNames + `)"\s*[,)]`)
	environmentDetectionProcessEnv = regexp.MustCompile(
		`process\.env(?:\.|\[\s*["'])(` + environmentDetectionNames + `)\b`)
	environmentDetectionShell = regexp.MustCompile(`\$\{?(` + environmentDetectionNames + `)\b`)
)

// environmentDetectionReads returns every listed variable one line reads.
func environmentDetectionReads(path, line string) []string {
	matchers := []*regexp.Regexp{environmentDetectionCall, environmentDetectionProcessEnv}
	if strings.HasSuffix(path, ".sh") {
		matchers = append(matchers, environmentDetectionShell)
	}
	var names []string
	for _, re := range matchers {
		for _, m := range re.FindAllStringSubmatch(line, -1) {
			names = append(names, m[1])
		}
	}
	return names
}

// TestNoEnvironmentSniffing fails on any read of a known environment-detection variable
// outside environmentDetectionAllowed, in Go, Buzz, shell, TypeScript and the markdown
// outside docs/ and changes/. Those two are the docs conventions target's half of the rule
// (TestDocsCheckReadsTheSameDetectionVariables keeps both halves on one list). Test files
// are exempt, since a test sets these to prove they are ignored.
func TestNoEnvironmentSniffing(t *testing.T) {
	t.Parallel()

	var files []string
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if environmentDetectionSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".test.ts") || path == "CHANGELOG.md" {
			return nil
		}
		// The docs pages and the changelog fragments are the docs project's to check
		// (docs/lib/crosscheck.buzz), so a page edit does not re-run this suite.
		slashed := filepath.ToSlash(path)
		if filepath.Ext(path) == ".md" && (strings.HasPrefix(slashed, "docs/") || strings.HasPrefix(slashed, "changes/")) {
			return nil
		}
		switch filepath.Ext(path) {
		case ".go", ".buzz", ".sh", ".ts", ".js", ".mjs", ".md":
			files = append(files, path)
		}
		return nil
	})
	require.NoError(t, err)

	found := make([][]string, len(files))
	var g errgroup.Group
	g.SetLimit(runtime.GOMAXPROCS(0))
	for i, path := range files {
		g.Go(func() error {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			slashed := filepath.ToSlash(path)
			for n, line := range strings.Split(string(data), "\n") {
				for _, name := range environmentDetectionReads(path, line) {
					if _, ok := environmentDetectionAllowed[slashed+":"+name]; ok {
						continue
					}
					found[i] = append(found[i], fmt.Sprintf("%s:%d: reads %s: %s",
						slashed, n+1, name, strings.TrimSpace(line)))
				}
			}
			return nil
		})
	}
	require.NoError(t, g.Wait())
	var violations []string
	for _, lines := range found {
		violations = append(violations, lines...)
	}

	assert.Empty(t, violations,
		"magus must not detect where it runs (docs/doctrine.md, \"Told, never guessed\").\n"+
			"Reading one of these variables to pick a default, an output or a provider makes the same\n"+
			"command behave differently on a laptop, a runner and inside an agent's shell, and nobody\n"+
			"can see why. Take a flag, a charm or a magus.yaml key instead, and have the caller pass\n"+
			"it: a workflow sets the variable or passes the flag on purpose. Code that reads one as\n"+
			"input after the caller chose it, switching nothing on it, goes in\n"+
			"environmentDetectionAllowed with its reason.\n\nviolations:\n%s",
		strings.Join(violations, "\n"))
}

// TestEnvironmentDetectionMatcher grades the matcher against lines, because a tree scan
// that finds nothing is equally consistent with a matcher that matches nothing.
func TestEnvironmentDetectionMatcher(t *testing.T) {
	for _, tc := range []struct {
		path, line string
		want       []string
	}{
		{"a.go", `if os.Getenv("GITHUB_ACTIONS") == "true" {`, []string{"GITHUB_ACTIONS"}},
		{"a.go", `v, ok := os.LookupEnv("CI")`, []string{"CI"}},
		{"a.go", `return getenv("GITHUB_ACTIONS") == "" && getenv("RUNNER_ENVIRONMENT") == ""`, []string{"GITHUB_ACTIONS", "RUNNER_ENVIRONMENT"}},
		{"a.buzz", `fun in_ci() > bool { return os\env("GITLAB_CI") == "true"; }`, []string{"GITLAB_CI"}},
		{"a.buzz", `final host = env\get("CLAUDECODE") catch "";`, []string{"CLAUDECODE"}},
		{"a.ts", `if (process.env.CI) {`, []string{"CI"}},
		{"a.sh", `[ -n "${CI:-}" ] && exit 0`, []string{"CI"}},

		// The terminal in front of the person is not where magus runs.
		{"a.go", `return os.Getenv("SSH_TTY") == ""`, nil},
		{"a.go", `if os.Getenv("TERM") != "dumb" {`, nil},
		{"a.go", `"ci": "CI", "json": "JSON",`, nil},
		{"a.go", `os.Getenv("CI_PROVIDER")`, nil},
		{"a.buzz", `final path = os\env("GITHUB_STEP_SUMMARY");`, nil},
		{"a.go", `tags: []string{"docker", "container", "image"},`, nil},
		{"a.buzz", `echo "$CI"`, nil},
	} {
		assert.Equal(t, tc.want, environmentDetectionReads(tc.path, tc.line),
			"environmentDetectionReads(%q, %q)", tc.path, tc.line)
	}
}

// mgsDeclarationFile declares and enumerates every diagnostic code. References there are
// the code EXISTING, not the code firing, so the raise-site scan looks past this one file.
const mgsDeclarationFile = "types/diagnostic.go"

// mgsCodeRe matches a diagnostic code written out as text, which is how a Buzz spell
// raises one: it throws the string, having no Go constant to reach for.
var mgsCodeRe = regexp.MustCompile(`MGS[0-9]{4}`)

// raiseSiteSkipDirs are trees a raise site cannot live in: version control and cache
// state, generated output, fixtures, and vendored code.
var raiseSiteSkipDirs = skipDirs("gen", "testdata")

// mgsCodesWithoutRaiseSite are the codes that fail TestEveryDiagnosticCodeHasARaiseSite
// today, listed rather than tolerated so the gate is green and the debt is named.
var mgsCodesWithoutRaiseSite = map[types.DiagnosticCode]string{}

// TestEveryDiagnosticCodeHasARaiseSite pins the property the code registry silently lost:
// a code magus can never emit.
//
// An enumerated code is a promise: it becomes a knowledge-graph node, a docs page, and a
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
		// raises MGS1016: a spell throws the code as a STRING, so there is no identifier
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

// commentSymbolAllowlist are references that deliberately name something absent. Keyed by
// file where cataloguing removals is the file's whole job, and by `file:pkg.sym` for a
// single exception. The value is WHY: an exception nobody can justify later is one that
// should never have been added.
var commentSymbolAllowlist = map[string]string{
	"internal/interp/runtime.go":                     "removedMagusfileAPI tables magus.needs, magus.ledger.clear and eight more calls magus no longer binds; absent on purpose is what the table says",
	"types/target.go:magus.Context":                  "names the dotted spelling precisely to say it is NOT valid Buzz type syntax",
	"types/target.go:magus.WithTargetNameNormalizer": "names the injection seam this change removed, which is the paragraph's subject",
	"spells/doc.go:types.SpellOp":                    "names the pre-split spelling to explain why spells.Op dropped the prefix",
	"vcs/jj.go:diff.files":                           "diff.files() belongs to jj's template language, which nothing here indexes",
}

// TestCommentsNameSymbolsThatExist scores every `pkg.Symbol` a Go comment names against
// what this module declares, and fails the ones that look like a rename left behind.
//
// A stale comment is worse than no comment: it is believed precisely because someone
// bothered to write it. MEASURED 2026-09-20 on the first run: four comments citing
// `internal/diff.PatchDigest` for a function that has lived in internal/changeset since
// that package was renamed, plus three from types.TrackDependencyWait ->
// types.WithDependencyWait, in files the rename had otherwise touched.
//
// Scored rather than asserted, because "this symbol does not exist" is not decidable
// here. That same run also turned up `diff.files()` (a function in jj's TEMPLATE
// language), `vcs.exe` (a Buzz host binding) and `vcs.go` (a filename). No index this
// module can build will ever resolve a foreign DSL, so a hard existence check would have
// to be narrowed by filters until it caught nothing, and every filter would blind it to
// a real class. Scoring keeps the loose match and ranks it instead: the two rules worth
// commentRefFailScore each describe a rename specifically, and anything below the
// threshold is printed for a reader to judge rather than enforced.
//
// What this CANNOT catch, stated so nobody trusts it further than it goes: a comment
// whose every symbol resolves and whose CLAIM is false. Two of that first batch also
// asserted behaviour already measured false, and no parser sees that. This is the
// mechanical half only.
func TestCommentsNameSymbolsThatExist(t *testing.T) {
	ix, files := buildSymbolIndex(t)
	require.NotEmpty(t, ix.byPkg, "parsed no packages; the walk stopped finding Go sources")

	var refs []commentRef
	for _, path := range files {
		slash := filepath.ToSlash(path)
		if _, allowed := commentSymbolAllowlist[slash]; allowed {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			continue // unparseable source is the compiler's finding, not this gate's
		}
		external := externalImports(f)
		for _, group := range f.Comments {
			for _, c := range group.List {
				for _, m := range commentRefRe().FindAllStringSubmatch(c.Text, -1) {
					pkg, sym := m[1], m[2]
					if external[pkg] || ix.resolves(pkg, sym) {
						continue
					}
					if _, allowed := commentSymbolAllowlist[slash+":"+pkg+"."+sym]; allowed {
						continue
					}
					score, why := ix.score(pkg, sym)
					if score == 0 {
						continue
					}
					refs = append(refs, commentRef{
						file: slash, line: fset.Position(c.Pos()).Line,
						pkg: pkg, sym: sym, score: score, why: why,
					})
				}
			}
		}
	}
	sort.SliceStable(refs, func(i, j int) bool { return refs[i].score > refs[j].score })

	var failed []string
	for _, r := range refs {
		line := fmt.Sprintf("%s:%d: %s.%s (score %d: %s)", r.file, r.line, r.pkg, r.sym, r.score, r.why)
		if r.score >= commentRefFailScore {
			failed = append(failed, line)
			continue
		}
		t.Log("below the threshold, judge it yourself: " + line)
	}
	assert.Empty(t, failed, "comments naming symbols this module no longer declares:\n%s",
		strings.Join(failed, "\n"))
}

var symbolIndexSkipDirs = skipDirs("worktrees", "gen", "vendor", "testdata", "dist")

// buildSymbolIndex walks the tree once and returns the index plus the Go files to scan.
//
// Packages sharing a name have their symbols UNIONED, and each is indexed under its
// directory name as well. Both widen what resolves, which is the conservative direction:
// a union can only hide a rotted reference, never invent one, and this gate would rather
// miss than accuse.
func buildSymbolIndex(t *testing.T) (symbolIndex, []string) {
	t.Helper()
	ix := newSymbolIndex()
	var files []string

	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable subtree is skipped, not fatal
		}
		if d.IsDir() {
			if symbolIndexSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		ix.files[d.Name()] = true
		if filepath.Ext(path) != ".go" {
			return nil
		}
		fset := token.NewFileSet()
		f, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return nil //nolint:nilerr // unparseable source is the compiler's finding, not this gate's
		}
		files = append(files, path)
		ix.add(f, filepath.Base(filepath.Dir(path)))
		return nil
	})
	require.NoError(t, err)
	return ix, files
}

// envNameLiteralRe matches a string literal that is exactly a MAGUS_* name, or a
// NAME=value pair handed to a child: the shapes a read (os.Getenv("NAME"), a const) and
// an export take. Prose naming a variable inside a longer message does not match.
var envNameLiteralRe = regexp.MustCompile(`^(MAGUS_[A-Z0-9_]*[A-Z0-9])(=.*)?$`)

// buzzEnvLiteralRe is envNameLiteralRe for a quoted Buzz string.
var buzzEnvLiteralRe = regexp.MustCompile("[\"`](MAGUS_[A-Z0-9_]*[A-Z0-9])(?:=[^\"`\\n]*)?[\"`]")

// envRegistrySkipDirs are trees whose MAGUS_* names are not reads: VCS and cache state,
// installed agent copies, dependencies, and fixtures. gen/ is walked on purpose, since the
// generated ApplyEnv is where every config-derived variable is read.
var envRegistrySkipDirs = skipDirs("testdata")

// envNamesNeverInEnvironment are MAGUS_* literals in shipped code that name something other
// than a variable magus reads, each with what it is. Registering one would admit a
// variable nothing reads.
var envNamesNeverInEnvironment = map[string]string{
	"MAGUS_QUEUE_APP_CLIENT_ID":   "a GitHub Actions variable the merge queue's GitHub provider sets up; workflows read it as vars.MAGUS_QUEUE_APP_CLIENT_ID",
	"MAGUS_QUEUE_APP_PRIVATE_KEY": "the GitHub Actions secret the merge queue's GitHub provider stores the queue app's key under; the setup-magus action reads it, magus never does",
}

// repoToolingPrefixes are the paths this repository builds and releases itself with,
// which ship to nobody. A MAGUS_* name only they read is theirs, not magus's.
var repoToolingPrefixes = []string{
	"cmd/magus-utils/", ".github/", "benchmarks/", "hack/", "magusfile.buzz",
}

// shipsWithMagus reports whether slash (a slash-separated repo path) is code that ships:
// not the repository's own tooling, and not the docs site except the agent glue a reader
// installs.
func shipsWithMagus(slash string) bool {
	for _, p := range repoToolingPrefixes {
		if strings.HasPrefix(slash, p) {
			return false
		}
	}
	return !strings.HasPrefix(slash, "docs/") || strings.HasPrefix(slash, "docs/guides/integrations/agents/")
}

// TestEveryReadMagusEnvVarIsRegistered holds config.EnvVarDocs complete for what ships, and
// keeps this repository's own tooling clear of MGS1046. A name shipped code reads and
// nobody registered is one doctor calls unknown and a near miss of it is not caught; a
// tooling name a typo away from a registered one would stop every magus command in the
// job that sets it.
func TestEveryReadMagusEnvVarIsRegistered(t *testing.T) {
	t.Parallel()

	found := map[string]string{}
	tooling := map[string]string{}
	note := func(name, where string) {
		into := found
		if !shipsWithMagus(where) {
			into = tooling
		}
		if _, ok := into[name]; !ok {
			into[name] = where
		}
	}
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if envRegistrySkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		slash := filepath.ToSlash(path)
		switch {
		case strings.HasSuffix(slash, ".buzz"):
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range buzzEnvLiteralRe.FindAllStringSubmatch(string(body), -1) {
				note(m[1], slash)
			}
		// retired.go names exactly what magus stopped reading; MGS1046 reports those by
		// name, so they are the one set that must NOT be registered.
		case strings.HasSuffix(slash, ".go") && !strings.HasSuffix(slash, "_test.go") && slash != "internal/config/retired.go":
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return nil //nolint:nilerr // a file that does not parse is the build's finding, not this gate's
			}
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				if m := envNameLiteralRe.FindStringSubmatch(s); m != nil {
					note(m[1], fmt.Sprintf("%s:%d", slash, fset.Position(lit.Pos()).Line))
				}
				return true
			})
		}
		return nil
	})
	require.NoError(t, err, "walk")
	require.Contains(t, found, "MAGUS_CACHE_DIR", "the scan found nothing it should have; it is broken, not green")

	var missing []string
	for name, where := range found {
		_, notEnv := envNamesNeverInEnvironment[name]
		assert.False(t, notEnv && config.KnownEnvVar(name), "%s is registered, so drop it from envNamesNeverInEnvironment", name)
		if !notEnv && !config.KnownEnvVar(name) {
			missing = append(missing, fmt.Sprintf("%s (%s)", name, where))
		}
	}
	for name := range envNamesNeverInEnvironment {
		assert.Contains(t, found, name, "%s is named nowhere any more; drop it from envNamesNeverInEnvironment", name)
	}
	slices.Sort(missing)
	assert.Empty(t, missing,
		"these MAGUS_* names are read or exported by shipped code but missing from config.EnvVarDocs;\n"+
			"register each with its doc, or rename it off the prefix:\n%s",
		strings.Join(missing, "\n"))

	var refused []string
	for name, where := range tooling {
		if msg, bad := config.EnvVarProblem(name); bad {
			refused = append(refused, fmt.Sprintf("%s (%s)", msg, where))
		}
	}
	slices.Sort(refused)
	assert.Empty(t, refused,
		"this repository's own tooling uses MAGUS_* names every magus command refuses (MGS1046);\n"+
			"rename them off the prefix:\n%s",
		strings.Join(refused, "\n"))
}

// hostSchemaDir holds the vendored agent-host schemas internal/agent's tests grade
// hook configs and rendered verdicts against, with the provenance of each in SOURCES.md.
const hostSchemaDir = "testdata/hosts"

// sourcesRow matches one row of the provenance table, which is the only place a
// vendored schema's origin and digest are written down.
var sourcesRow = regexp.MustCompile("(?m)^\\| `([^`]+)` \\| (published|derived-from-binary|derived) \\|.*\\| `([0-9a-f]{64})` \\|")

// TestVendoredHostSchemasMatchTheirRecordedDigest keeps the provenance honest.
//
// The whole value of a vendored schema is that it is the host's bytes and not our
// opinion of them. Nothing else stops a failing gate from being "fixed" by editing the
// schema, which would leave the test green and the claim false.
func TestVendoredHostSchemasMatchTheirRecordedDigest(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(hostSchemaDir, "SOURCES.md"))
	require.NoError(t, err, "read the provenance table")

	recorded := map[string]string{}
	for _, row := range sourcesRow.FindAllStringSubmatch(string(body), -1) {
		recorded[row[1]] = row[3]
	}
	require.NotEmpty(t, recorded, "SOURCES.md must carry a row per vendored schema")

	vendored, err := filepath.Glob(filepath.Join(hostSchemaDir, "*", "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, vendored)

	for _, path := range vendored {
		rel := filepath.ToSlash(strings.TrimPrefix(path, hostSchemaDir+string(filepath.Separator)))
		want, ok := recorded[rel]
		require.True(t, ok, "%s is vendored but SOURCES.md says nothing about where it came from", rel)
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		sum := sha256.Sum256(raw)
		assert.Equal(t, want, hex.EncodeToString(sum[:]),
			"%s no longer matches the digest SOURCES.md records. If you refreshed it, update the\n"+
				"digest and the read date in the same commit. If you edited it to make a gate pass,\n"+
				"that gate was telling you a shipped file drifted from what its host accepts.", rel)
	}

	for rel := range recorded {
		assert.FileExists(t, filepath.Join(hostSchemaDir, filepath.FromSlash(rel)),
			"SOURCES.md records %s, which is not vendored", rel)
	}
}

// storeMechanicsAllowed maps "<path>:<func>" to why that function still hand-rolls a
// temp-file rename or takes an flock itself instead of calling internal/file. It only
// shrinks: TestStoreMechanicsLiveInInternalFile fails on an entry whose site is gone.
var storeMechanicsAllowed = map[string]string{
	"internal/cache/artifact.go:copyBlob":                    "streams a blob into the CAS, hashing as it copies",
	"internal/cache/snapshot.go:Cache.snapshotOne":           "streams a blob into the CAS, hashing as it copies",
	"internal/maintenance/build_lock.go:AcquireGraphBuild":   "a graph build lock waits on the build it names and is held across a whole build",
	"internal/queue/verdicts.go:VerdictDir.WritePlan":        "publishes a directory, not a file",
	"internal/queue/verdicts.go:VerdictDir.RecordWithBundle": "publishes a directory, not a file",
	"lock.go:projectLocker.acquire":                          "a project lock waits unbounded with a heartbeat: its holder is a build",
	"lock.go:lockIsHeld":                                     "probes a project lock without holding it",
	"pipe.go:GatePipe":                                       "a project lock waits unbounded with a heartbeat: its holder is a build",
	"pipe.go:projectLocker.publishHolds":                     "a project lock waits unbounded with a heartbeat: its holder is a build",
}

// storeMechanicsSkipDirs are trees the rule does not govern. libs/ holds modules of their
// own, which cannot import internal/file.
var storeMechanicsSkipDirs = skipDirs("gen", "testdata")

// TestStoreMechanicsLiveInInternalFile holds the file-persistence mechanics to one
// implementation. A temp file renamed into place and an flock-guarded read-modify-write
// each had several copies that differed in fsync, file mode, temp cleanup and whether the
// wait honored ctx; internal/file (WriteFileAtomic, ReplaceFile, WithLock, Doc) is the one
// copy. The scan is per function and textual, so a copy split across two functions slips
// past it; what it catches is the shape every copy so far has had.
func TestStoreMechanicsLiveInInternalFile(t *testing.T) {
	t.Parallel()

	var files []string
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		slashed := filepath.ToSlash(path)
		if d.IsDir() {
			if storeMechanicsSkipDirs[d.Name()] || slashed == "libs" || slashed == "internal/file" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	require.NoError(t, err)

	found := make([][]string, len(files))
	var g errgroup.Group
	g.SetLimit(runtime.GOMAXPROCS(0))
	for i, path := range files {
		g.Go(func() error {
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if !strings.Contains(string(src), "Rename(") && !strings.Contains(string(src), ".New(") {
				return nil
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			found[i] = storeMechanicsSites(filepath.ToSlash(path), f)
			return nil
		})
	}
	require.NoError(t, g.Wait())

	seen := map[string]bool{}
	var violations []string
	for _, sites := range found {
		for _, site := range sites {
			seen[site] = true
			if _, ok := storeMechanicsAllowed[site]; !ok {
				violations = append(violations, site)
			}
		}
	}
	var stale []string
	for site := range storeMechanicsAllowed {
		if !seen[site] {
			stale = append(stale, site)
		}
	}
	sort.Strings(violations)
	sort.Strings(stale)

	assert.Empty(t, violations,
		"these functions write a file by temp and rename, or take an flock, themselves.\n"+
			"Use internal/file: WriteFileAtomic or ReplaceFile to replace a file, WithLock to\n"+
			"hold a lock, Doc.Update for a locked read-modify-write. A site that truly cannot\n"+
			"(it streams while hashing, publishes a directory, waits unbounded) goes in\n"+
			"storeMechanicsAllowed with its reason.\n\nviolations:\n%s", strings.Join(violations, "\n"))
	assert.Empty(t, stale,
		"storeMechanicsAllowed names sites that no longer hand-roll these mechanics; delete\n"+
			"their entries so the list only shrinks:\n%s", strings.Join(stale, "\n"))
}

// TestStoreMechanicsMatcher grades the scan against sources, because a tree scan that
// finds nothing is equally consistent with a matcher that matches nothing.
func TestStoreMechanicsMatcher(t *testing.T) {
	src := `package p

import (
	osx "os"

	"github.com/gofrs/flock"
)

func handRolled(p string) error {
	f, _ := osx.CreateTemp(".", "x")
	_ = f.Close()
	return osx.Rename(f.Name(), p)
}

func (s *store) locks() { _ = flock.New("x") }

func renameOnly(a, b string) error { return osx.Rename(a, b) }

func tempOnly() { _, _ = osx.CreateTemp(".", "x") }
`
	f, err := parser.ParseFile(token.NewFileSet(), "p.go", src, parser.SkipObjectResolution)
	require.NoError(t, err)
	assert.Equal(t, []string{"p.go:handRolled", "p.go:store.locks"}, storeMechanicsSites("p.go", f))
}
