// cross-cutting: repo-wide conventions scanned across the whole tree, owned by no one file

package magus

// Source-level convention guards: assertions about the SHAPE of this repository
// rather than the behavior of any symbol in it. They scan the tree as text because
// what they prevent has no runtime signal to observe.
//
// None of them pairs with a source file, and that is the point rather than an
// oversight: their subject is an agreement across artifacts, not a Go symbol. They
// live together so the tree carries one file named for that subject instead of four
// named after nothing, which is how hookdocs_test.go once advertised a hookdocs.go
// that never existed.

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"

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

// Dogfooding checks: assertions that this repository actually uses what it publishes.
//
// This file deliberately has no dogfood.go beside it, and it is the one place in the tree
// where that is the point rather than an oversight. Its subject is not a Go symbol but the
// agreement between three artifacts (the repo's own config, the guide, and the templates a
// reader downloads), so there is nothing for it to pair with, and naming it after any one of
// them (it was hookdocs_test.go) advertised a hookdocs.go that never existed. Anything else
// asserting "we use what we ship" belongs here too.
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

	// Every event, not only the pre-tool ones. A checkpoint or a rehydration hook
	// inlining its own copy of a template drifts from the file a reader downloads
	// exactly as a guard hook would, and used to do so unwatched.
	for event, entries := range cfg.Hooks {
		for _, entry := range entries {
			require.NotEmpty(t, entry.Hooks, "%s matcher %q has no hooks", event, entry.Matcher)
			for _, h := range entry.Hooks {
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

// magus is agent-host agnostic, and this test is the only thing that enforces it.
// The rule was written down twice: in docs/guides/integrations/agents.md ("magus
// owns the guard rules and the verdict, not integration code for each host") and
// in the skill-authoring skill ("no host name appears in code"), and honored
// nowhere mechanically, which is how `magus mcp` came to print Codex and Claude
// Desktop setup instructions. A change to any one of those clients then meant a
// magus release.
//
// The rule, precisely: a host's NAME may appear only as part of a filesystem
// path, because naming the directory a host discovers skills in is the one
// host-specific step magus is allowed to know about (agents.md says so). Anywhere
// else (prose, help text, printed setup instructions, a per-host branch), the
// host-specific part belongs in documentation the reader owns.

// hostNames are agent hosts magus must not encode behavior for. Cursor is a
// supported host too and is deliberately absent here, because the bare word is a
// terminal position in internal/interactive and a pagination token in the graph
// query and MCP handlers: 209 lines in this tree use it innocently, against the one
// that names the host. cursorHostUse carries it instead.
var hostNames = regexp.MustCompile(`(?i)\b(claude|opencode|codex|aider|windsurf)\b`)

// cursorHostUse matches the shapes "cursor" takes when it means the HOST: the proper
// noun in prose, a phrase naming the host's own machinery, and a comparison against
// the host label. It flags nothing an editor cursor or a page cursor produces, so it
// needs no exemption list at all, which an allowlist of the legitimate identifiers
// would have needed and would have gone stale on the next paging field.
//
// The trade it makes: a bare `case "cursor":` is NOT matched, because two switches in
// this tree already have one (a diff-session op and a memory op) and no line-level
// pattern separates those from a host switch. A host branch written that way slips
// through; every other shape does not.
var cursorHostUse = regexp.MustCompile(`\(Cursor\)|(?i:\bcursor (hooks?|ide|editor|rules)\b|[!=]=\s*"cursor")`)

// hostSpecificLine reports whether one line of Go source names an agent host outside
// a filesystem path.
func hostSpecificLine(text string) bool {
	if cursorHostUse.MatchString(text) {
		return true
	}
	// Strip every path-shaped use, then re-test: a line may legitimately carry both
	// (an example destination plus surrounding prose).
	return hostNames.MatchString(text) && hostNames.MatchString(hostPathUse.ReplaceAllString(text, ""))
}

// hostPathUse allows a host name that names something ON DISK: a path
// (`.claude/skills`, `~/.config/opencode/skills`, `.codex/config.toml`) or a bare
// quoted filename stem (`case "agents", "claude":` classifying AGENTS.md and
// CLAUDE.md). Recognizing a well-known file is the sanctioned exception: it is a
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

// The walk names the files and the scanning is fanned out across them: three regexes per
// LINE over every non-test .go file in the tree is the package's longest test, and under
// -race a single goroutine spends that time alone while the other cores idle.
func TestNoHostSpecificBehaviorInCode(t *testing.T) {
	t.Parallel()

	var files []string
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
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	// Indexed by file rather than appended under a mutex: the result is in walk order
	// whatever order the scheduler finishes in, so a failure reads the same way twice
	// without a sort, and there is no shared slice to guard.
	found := make([][]string, len(files))
	var g errgroup.Group
	g.SetLimit(runtime.GOMAXPROCS(0))
	for i, path := range files {
		g.Go(func() error {
			lines, err := hostSpecificLines(path)
			found[i] = lines
			return err
		})
	}
	if err := g.Wait(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	var violations []string
	for _, lines := range found {
		violations = append(violations, lines...)
	}

	assert.Empty(t, violations,
		"magus must not encode agent-host specifics.\n"+
			"A host name is allowed only inside a filesystem path (e.g. .claude/skills), because naming\n"+
			"the directory a host reads is the one host-specific step magus owns. Everything else - setup\n"+
			"instructions, help text, a per-host branch - belongs in docs the reader owns, or the next\n"+
			"change to that host becomes a magus release.\n\nviolations:\n%s",
		strings.Join(violations, "\n"))
}

// hostSpecificLines is every line of one file that names a host, located for a reader.
func hostSpecificLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 1; sc.Scan(); line++ {
		text := sc.Text()
		if !hostSpecificLine(text) {
			continue
		}
		out = append(out, fmt.Sprintf("%s:%d: %s", path, line, strings.TrimSpace(text)))
	}
	return out, sc.Err()
}

// TestHostSpecificLineMatcher grades the matcher against lines rather than against the
// tree, because a tree scan that finds nothing is equally consistent with a matcher that
// matches nothing. Cursor is the case that needs it: the name went unenforced for its
// whole life as a supported host, and the reason it stays hard is right here in the
// negative cases.
func TestHostSpecificLineMatcher(t *testing.T) {
	for _, tc := range []struct {
		line string
		want bool
	}{
		{`if host == "cursor" {`, true},
		{`return h.Host != "cursor"`, true},
		{`// Cursor hooks fire after the write, not before it.`, true},
		{`// on a host with no pre-write file hook (Cursor), the deny lands late`, true},
		{`fmt.Println("paste this into your Claude settings")`, true},

		{`cursor := paramString(req.Params, "cursor", "")`, false},
		{`// Cursor reports where the cursor is, in 1-based terminal coordinates.`, false},
		{"\tCursor DiffCursor `json:\"cursor\" yaml:\"cursor\"`", false},
		{`"cursor-hook.sh",`, false},
		{`filepath.Join(root, ".cursor", "hooks.json"),`, false},
		{`filepath.Join(root, ".claude", "settings.json"),`, false},
	} {
		assert.Equal(t, tc.want, hostSpecificLine(tc.line), "hostSpecificLine(%q)", tc.line)
	}
}

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

// environmentDetectionSkipDirs are trees the rule does not govern: generated output,
// vendored code, fixtures, and history. docs is scanned, because the agent glue a reader
// installs lives there; its pages are checked by the docs project instead.
var environmentDetectionSkipDirs = map[string]bool{
	".git": true, ".magus": true, ".claude": true, ".agents": true, ".opencode": true,
	"node_modules": true, "gen": true, "testdata": true, "blog": true, "releases": true,
	"manpage": true, "schema": true,
}

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

// The test above is one layer shallower than the rule it enforces. A branch keyed
// on a host's TOOL VOCABULARY rather than its name (`switch tool { case "Read":
// ... case "Bash": }`) is a per-host branch in everything but spelling, and no
// host name appears in it, so the name scan waves it through. The test below is
// that second layer.

// hostToolVocabularyScope is where this rule bites: the guard's own source, the
// only code that ever sees a host's hook payload. Scoped rather than tree-wide
// because "Read", "Write" and "Task" are ordinary words elsewhere in this module
// (method names, struct fields, JSON tags, graph kinds, spell ops), and a tree-wide
// scan would report hundreds of them and be turned off within the week. A per-host
// branch that is not deciding a verdict is not the failure this exists to prevent.
var hostToolVocabularyScope = []string{
	filepath.Join("internal", "guard", "*.go"),
	filepath.Join("cmd", "magus", "shell*.go"),
	filepath.Join("cmd", "magus", "guard_*.go"),
	filepath.Join("internal", "agent", "*.go"),
}

// hostToolVocabulary is what agent hosts call their tools. magus's own labels
// (hookToolCommand, hookToolWrite, hookToolRead in internal/guard/guard.go) are
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
// comment in this repo, so an entry here (reviewable, and readable as a list) is
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
// explains the rule (including guard.go's own comment, which quotes "Read" and
// "Bash") is not itself a violation.
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
			"hookToolWrite, hookToolRead in internal/guard/guard.go) and let the wrapper in the reader's own\n"+
			"config do the mapping - which flag it passes IS the mapping. If a literal genuinely has to\n"+
			"be here, add it to hostToolVocabularyByDesign with where that decision is written down.\n\n"+
			"violations:\n%s",
		strings.Join(violations, "\n"))
}

// verdictTextPrefix opens every reason and every advisory the guard produces, so a
// literal carrying it is a RULE wherever it sits.
const verdictTextPrefix = "magus workspace:"

// TestTheShellCommandCarriesNoRuleText keeps the rules on the importable side of the
// split. A rule written into cmd/magus/shell.go would work, and would be invisible to
// both the rule suite and the replay path that re-grades a recorded command, because
// neither can reach package main. Nothing else marks which side a new rule belongs on,
// and a boundary that lives only in prose is one with roughly even odds.
func TestTheShellCommandCarriesNoRuleText(t *testing.T) {
	fset := token.NewFileSet()
	const path = "cmd/magus/shell.go"
	f, err := parser.ParseFile(fset, path, nil, 0)
	require.NoErrorf(t, err, "parse %s: the guard's CLI half moved and this gate stopped looking", path)

	var violations []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, uerr := strconv.Unquote(lit.Value)
		if uerr != nil || !strings.HasPrefix(value, verdictTextPrefix) {
			return true
		}
		violations = append(violations, fmt.Sprintf("%s: %s", fset.Position(lit.Pos()), lit.Value))
		return true
	})

	assert.Empty(t, violations,
		"a guard rule may not live in %s. Rules belong in internal/guard, where the rule suite\n"+
			"and `magus session ls`'s replay path can both reach them; this file owns flags, stdin and\n"+
			"rendering only.\n\nviolations:\n%s",
		path, strings.Join(violations, "\n"))
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
	"internal/render/target_graph.go",
	"cmd/magus-docs/main.go",
	"internal/observability/otlp/provider.go",
	"internal/handler/mcp/registry.go",
	"internal/handler/mcp/output.go",
	"internal/handler/mcp/where.go",
	"cmd/magus/query.go",
	"internal/guard/shell.go",
	"internal/guard/write.go",
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

// TestUserFacingStringsAreASCII scans string literals (not comments; CLAUDE.md
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

// mgsDeclarationFile declares and enumerates every diagnostic code. References there are
// the code EXISTING, not the code firing, so the raise-site scan looks past this one file.
const mgsDeclarationFile = "types/diagnostic.go"

// mgsCodeRe matches a diagnostic code written out as text, which is how a Buzz spell
// raises one: it throws the string, having no Go constant to reach for.
var mgsCodeRe = regexp.MustCompile(`MGS[0-9]{4}`)

// raiseSiteSkipDirs are trees a raise site cannot live in: version control and cache
// state, generated output, fixtures, and vendored code.
var raiseSiteSkipDirs = map[string]bool{
	".git": true, ".magus": true, ".claude": true, ".agents": true, ".opencode": true,
	"node_modules": true, "gen": true, "testdata": true,
}

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

// TestTypesStaysPureDomain gives types/doc.go's contract an enforcement point. It said
// "no filesystem, VCS, or process-execution dependencies" and nothing checked it, so the
// rule lived on whoever last read the file.
//
// What it forbids is REACHING THE WORLD: opening a file, running a process, dialing a
// host, asking a VCS. What it permits is string work over paths and host:port, which is
// why path/filepath and net are here rather than banned. types.Path resolves and
// relativizes (types/path.go:40) and types.SecretGrant splits a host (types/secret.go:138);
// both are pure computation over values a caller already had, and banning them would push
// path arithmetic into every caller that has a Path.
//
// magus's own packages are forbidden with one exception, internal/json, and the exception
// is not a compromise: TestNoDirectEncodingJSONImport REQUIRES every package that encodes
// JSON to use it, so banning it here would leave types unable to marshal at all. It is a
// codec over encoding/json/v2 with no I/O of its own.
//
// CLAUDE.md says types imports "only spells and libs/diagnostics", which is stricter than
// the contract and has been false since types learned to marshal. The doc comment is the
// rule; this test is what makes it one.
func TestTypesStaysPureDomain(t *testing.T) {
	t.Parallel()
	forbidden := []string{
		"os", "os/exec", "io/fs", "net/http", "database/sql", "os/user",
		"github.com/egladman/magus/vcs",
		"github.com/egladman/magus/project",
	}
	allowedMagus := []string{
		"github.com/egladman/magus/spells",
		"github.com/egladman/magus/libs/diagnostics",
		"github.com/egladman/magus/internal/json",
		"github.com/egladman/magus/types/enum",
	}

	entries, err := os.ReadDir("types")
	require.NoError(t, err)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join("types", e.Name())
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		require.NoError(t, err, "parsing %s", path)
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			assert.NotContains(t, forbidden, p,
				"%s imports %q: types is the near-leaf domain package and may not reach the world", path, p)
			if strings.HasPrefix(p, "github.com/egladman/magus/") {
				assert.Contains(t, allowedMagus, p,
					"%s imports %q: types may depend on spells, libs/diagnostics, internal/json and types/enum, nothing else in magus", path, p)
			}
		}
	}
}

// cmdMagusInternalCeiling is the number of internal/ packages cmd/magus imports today.
// It is a RATCHET: the number may fall, never rise.
//
// Measured over this repository's history, it was 25 in May, 36 in July, 48 in August and
// 63 in September, while cmd/magus's references to the root package rose 71 -> 189 over
// the same span. Both doors into the engine are widening, and the cost is not abstract:
// the concurrency clamp exists in cmd/magus/main.go AND magus.go, with a comment in the
// former admitting the latter "never runs". It is 67 now because the merge queue's four
// packages moved from libs/mergequeue to internal/queue: cmd/magus imported them already,
// and only the prefix this test counts changed.
//
// This test decides nothing about which door is right. It only stops the drift being
// invisible. Lowering the number is the win; raising it should be a sentence in a commit
// message explaining why the composition root could not hold the new dependency.
const cmdMagusInternalCeiling = 67

func TestCmdMagusInternalImportsOnlyShrink(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(filepath.Join("cmd", "magus"))
	require.NoError(t, err)

	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join("cmd", "magus", e.Name())
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		require.NoError(t, err, "parsing %s", path)
		for _, imp := range f.Imports {
			if p := strings.Trim(imp.Path.Value, `"`); strings.HasPrefix(p, "github.com/egladman/magus/internal/") {
				seen[p] = true
			}
		}
	}

	assert.LessOrEqual(t, len(seen), cmdMagusInternalCeiling,
		"cmd/magus now imports %d internal packages, over the %d ceiling: put the new dependency behind the root package, "+
			"or lower the ceiling deliberately and say why", len(seen), cmdMagusInternalCeiling)
}

// establishedCompoundNames are Go filename segments that LOOK like two words mashed
// together and are single established terms. They are exempt from the check below.
//
// An allowlist rather than a cleverer test, because no rule distinguishes "runtime" from
// "pushgate": both are two known words with the separator dropped, and only a person
// knows the first is a word and the second is a mistake. Adding an entry is the deliberate
// act of saying "this is one word"; it is not a place to park a name you did not want to
// think about.
var establishedCompoundNames = map[string]bool{
	"runtime": true, // Go's own term
	"stdlib":  true,
	"keyring": true,
	"jsonv2":  true, // names the GOEXPERIMENT
	"libproc": true, // the Darwin API
	"vmstat":  true, // the Darwin tool
	// GNU make's name for the token protocol internal/proc/run implements.
	"jobserver": true,
}

// grandfatheredCompoundNames are concatenations already in the tree when this check
// landed. They are NOT exemptions: each is a rename waiting for a session with room for
// it, and the list is meant to shrink.
//
// Recorded rather than fixed on the spot because a rename touches every importer, and a
// gate that forced twenty of them at once would be turned off instead of satisfied.
var grandfatheredCompoundNames = map[string]bool{
	"magusfile":   true, // the file it names is called magusfile.buzz, so this may be right
	"eventstream": true,
	"hostmodules": true,
	"promptcache": true,
	"selfupdate":  true,
	"toolref":     true,
}

// TestGoFileNamesDoNotMashWordsTogether keeps new filenames readable: one word, or words
// separated by an underscore the way workspace_shell.go and prompt_cache.go do it. Never
// wordsmashedtogether.
//
// The vocabulary is built FROM THE TREE, which is what makes this checkable without a
// dictionary: a segment is suspect when it splits into two segments this repository
// already uses as filenames. skillgate is skill plus gate, pushgate is push plus gate,
// hostschemas is hosts plus schemas -- all three shipped in one session, each one after
// the last had been corrected by hand, which is the argument for a gate over a habit.
//
// Deliberately narrow: it can only see a mash of two words the tree already knows, so it
// misses a compound of words that appear nowhere else. A check that catches the common
// case and never lies is worth more than one that tries to catch everything.
func TestGoFileNamesDoNotMashWordsTogether(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Dir(thisFile)

	suffixes := regexp.MustCompile(`_(test|linux|darwin|windows|unix|other|amd64|arm64|js|wasm|freebsd|openbsd|netbsd)$`)
	segments := map[string]bool{}
	type goFile struct{ path, base string }
	var files []goFile

	require.NoError(t, filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == "node_modules" || name == ".claude" || name == "gen" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		base := strings.TrimSuffix(name, ".go")
		for prev := ""; prev != base; {
			prev, base = base, suffixes.ReplaceAllString(base, "")
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		files = append(files, goFile{path: rel, base: base})
		for _, seg := range strings.Split(base, "_") {
			if seg != "" {
				segments[seg] = true
			}
		}
		return nil
	}))
	require.NotEmpty(t, files, "walked no Go files; this gate went quiet rather than red")

	for _, f := range files {
		for _, seg := range strings.Split(f.base, "_") {
			if establishedCompoundNames[seg] || grandfatheredCompoundNames[seg] {
				continue
			}
			for i := 2; i < len(seg)-1; i++ {
				head, tail := seg[:i], seg[i:]
				if !segments[head] || !segments[tail] {
					continue
				}
				assert.Fail(t, "filename mashes two words together",
					"%s: %q is %q + %q, both of which this repository already uses as filenames.\n"+
						"Name it %s.go, or %s_%s.go if it genuinely covers both. If %q is one established word,\n"+
						"add it to establishedCompoundNames and say why.",
					f.path, seg, head, tail, tail, head, tail, seg)
				break
			}
		}
	}
}

// symbolIDPackage pulls the package path and symbol name out of a SCIP symbol ID, whose
// shape is `symbol:gomod <module> ` + "`" + `<package path>` + "`" + `/<name>...`.
//
// Parsed rather than recomputed from the filesystem: the package a symbol belongs to and
// the name it carries are facts the index already holds, and deriving them again from
// paths would be a second answer to drift from the first.
var symbolIDPackage = regexp.MustCompile("^symbol:gomod \\S+ `([^`]+)`/(.+)$")

// TestExportedNamesDoNotStutter reads the SYMBOL GRAPH, not the filesystem.
//
// This is the check that cannot be written any other way. Stutter is a property of a
// package name together with a symbol name (sessions.SessionAdapter reads as
// sessions.Session... at every call site), and a test that walked files would have the
// filenames and none of the symbols. magus indexes both, so the question is a query.
//
// It SKIPS when the index has nothing for this module, and that is deliberate rather than
// lenient: the symbol index is built by the scip op and is stale or absent until it runs,
// so a test that quietly passed on an empty index would report "no stutter" for a tree it
// never read. Skipping says which.
func TestExportedNamesDoNotStutter(t *testing.T) {
	ctx := context.Background()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Dir(thisFile)

	ws, err := Inspect(ctx, root)
	require.NoError(t, err)
	g, err := BuildKnowledgeGraph(ctx, ws, root, config.Config{}, false, slog.Default())
	require.NoError(t, err)
	require.NoError(t, MergeWorkspaceSymbols(ctx, ws, root, config.Config{}, g, slog.Default()))

	const module = "github.com/egladman/magus"
	found := map[string][]string{}
	indexed := 0
	for _, n := range g.Nodes() {
		if n.Kind != types.KindSymbol {
			continue
		}
		m := symbolIDPackage.FindStringSubmatch(n.ID)
		if m == nil || !strings.HasPrefix(m[1], module) {
			continue
		}
		indexed++
		// The name is everything before the first descriptor suffix SCIP appends: `.`
		// for a term, `()` for a method, `#` for a type.
		name := m[2]
		for _, cut := range []string{".", "(", "#", "/"} {
			if i := strings.Index(name, cut); i >= 0 {
				name = name[:i]
			}
		}
		pkg := m[1][strings.LastIndex(m[1], "/")+1:]
		if name == "" || !unicode.IsUpper(rune(name[0])) || len(pkg) < minStutterPackage {
			continue
		}
		if len(name) > len(pkg) && strings.EqualFold(name[:len(pkg)], pkg) {
			found[pkg] = append(found[pkg], name)
		}
	}

	if indexed == 0 {
		t.Skip("no symbols indexed for this module: run `magus graph build`, which is what this reads")
	}
	for pkg, names := range found {
		if stutterAllowed[pkg] {
			continue
		}
		slices.Sort(names)
		assert.Fail(t, "exported names stutter against their package",
			"package %q exports %v, which read as %s.%s... at every call site.\n"+
				"Drop the package name from the symbol, or add %q to stutterAllowed and say why.",
			pkg, slices.Compact(names), pkg, pkg, pkg)
	}
}

// minStutterPackage is the shortest package name worth testing. A two-letter package
// shares a prefix with too many ordinary words for the match to mean anything.
const minStutterPackage = 3

// stutterAllowed are packages whose exported names repeat the package name on purpose.
var stutterAllowed = map[string]bool{}

// TestNameOutputGoesThroughEmitNames keeps `-o name` on the structured-output
// destination. writeFormatted cannot render outputName, so every command answers
// that format in its own switch arm, and an arm that reaches for fmt.Println prints
// to stdout directly: --tee accepts the flag, writes nothing, and says nothing.
// Twenty-five arms had drifted that way before emitNames existed to point at.
func TestNameOutputGoesThroughEmitNames(t *testing.T) {
	paths, err := filepath.Glob("cmd/magus/*.go")
	require.NoError(t, err)
	require.NotEmpty(t, paths, "cmd/magus moved and this gate stopped looking")

	fset := token.NewFileSet()
	files := make([]*ast.File, 0, len(paths))
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		require.NoErrorf(t, perr, "parse %s", path)
		files = append(files, f)
	}

	emitters := nameEmitters(files)
	var violations []string
	arms := 0
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			clause, ok := n.(*ast.CaseClause)
			if !ok || !casePicks(clause, "outputName") {
				return true
			}
			arms++
			if !callsAny(clause.Body, emitters) {
				violations = append(violations, fset.Position(clause.Pos()).String())
			}
			return true
		})
	}

	require.NotZero(t, arms, "no `case outputName:` arm found: the format constant was renamed")
	assert.Empty(t, violations,
		"every `case outputName:` arm must render through emitNames or emitNamesOf.\n"+
			"Printing to stdout directly bypasses --tee, which then accepts the flag and writes an\n"+
			"empty file. A single value is emitNames([]string{v}); a slice of records is\n"+
			"emitNamesOf(records, func(r T) string { return r.Field }).\n\narms:\n%s",
		strings.Join(violations, "\n"))
}

// casePicks reports whether the clause selects exactly the given identifier, so
// `case outputJSON, outputName:` is not read as a name arm.
func casePicks(clause *ast.CaseClause, name string) bool {
	if len(clause.List) != 1 {
		return false
	}
	id, ok := clause.List[0].(*ast.Ident)
	return ok && id.Name == name
}

// nameEmitters returns every function in the package that reaches the structured-output
// destination, seeded with the three that ARE it and closed under calls.
//
// The closure is what keeps this from becoming an allowlist. Several arms delegate to a
// helper of their own (emitProjectNames, diffNames) which is correct and which a check
// looking for a literal emitNames call reports as a violation; growing a list of blessed
// helper names instead would go stale the first time somebody adds a fourth.
func nameEmitters(files []*ast.File) map[string]bool {
	emitters := map[string]bool{"emitNames": true, "emitNamesOf": true, "outputDst": true}
	for changed := true; changed; {
		changed = false
		for _, f := range files {
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil || emitters[fn.Name.Name] {
					continue
				}
				if callsAny(fn.Body.List, emitters) {
					emitters[fn.Name.Name] = true
					changed = true
				}
			}
		}
	}
	return emitters
}

// callsAny reports whether any statement calls one of the named functions directly.
func callsAny(stmts []ast.Stmt, names map[string]bool) bool {
	found := false
	for _, stmt := range stmts {
		ast.Inspect(stmt, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok && names[id.Name] {
				found = true
				return false
			}
			return true
		})
	}
	return found
}

// commentRefRe finds `pkg.Symbol` inside comment prose: a lowercase package name, a
// dot, and an identifier of ANY casing.
//
// Casing is deliberately not a filter. A lowercase helper renamed out from under its
// comment is the exact rot this gate exists to catch, so requiring an internal capital
// would make that class permanently invisible to buy tidier output. The noise a loose
// match admits is handled by SCORING it below, where a reader can weigh it.
var commentRefRe = regexp.MustCompile(`\b([a-z][a-z0-9]*)\.([A-Za-z_][A-Za-z0-9_]*)\b`)

// buzzNameRe matches a Buzz member as source spells it, with the backslash Buzz uses to
// qualify: `vcs\commit`, and the namespaced `magus\secret.read`.
var buzzNameRe = regexp.MustCompile(`\b([a-z][a-z0-9]*)\\([A-Za-z_][A-Za-z0-9_]*)(\.[A-Za-z_][A-Za-z0-9_]*)?`)

// commentSymbolAllowlist are references that deliberately name something absent. Keyed by
// file where cataloguing removals is the file's whole job, and by `file:pkg.sym` for a
// single exception. The value is WHY: an exception nobody can justify later is one that
// should never have been added.
var commentSymbolAllowlist = map[string]string{
	"internal/interp/runtime.go":                     "removedMagusfileAPI tables magus.needs, magus.ledger.clear and eight more calls magus no longer binds; absent on purpose is what the table says",
	"types/target.go:magus.Context":                  "names the dotted spelling precisely to say it is NOT valid Buzz type syntax",
	"types/target.go:magus.WithTargetNameNormalizer": "names the injection seam this change removed, which is the paragraph's subject",
	"spells/doc.go:types.SpellOp":                    "names the pre-split spelling to explain why spells.Op dropped the prefix",
	"conventions_test.go:sessions.SessionAdapter":    "an invented example of stutter, which has to read as the thing it warns about",
	"vcs/jj.go:diff.files":                           "diff.files() belongs to jj's template language, which nothing here indexes",
}

// commentRefFailScore is where a scored reference stops being a suspicion and becomes a
// finding. Both rename signatures below are worth exactly this on their own, so the
// threshold says: fail when something looks like a rename, never on smell alone.
const commentRefFailScore = 5

// commentRef is one `pkg.Symbol` this module does not declare, with the evidence that it
// is a rotted reference rather than prose.
type commentRef struct {
	file  string
	line  int
	pkg   string
	sym   string
	score int
	why   string
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
				for _, m := range commentRefRe.FindAllStringSubmatch(c.Text, -1) {
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

// symbolIndex is what this module declares, indexed the ways a COMMENT cites it rather
// than the way the compiler resolves it. Prose writes "internal/diff.PatchDigest" and
// "magus.diagnoseDrift"; neither is a Go selector, and both name something real.
type symbolIndex struct {
	byPkg    map[string]map[string]bool // package name and directory name alike
	exported map[string]string          // exported symbol -> its one package, "" when several
	host     map[string]bool            // dotted names the Buzz surface exposes
	files    map[string]bool            // every base filename in the tree
}

// resolves reports whether this module accounts for the reference at all.
func (ix symbolIndex) resolves(pkg, sym string) bool {
	if ix.host[pkg+"."+sym] || ix.files[pkg+"."+sym] {
		return true
	}
	syms, ours := ix.byPkg[pkg]
	return !ours || syms[sym]
}

// score rates how much an unresolved reference reads like a rename rather than prose,
// and names the evidence. Zero means no evidence at all, which is not reported.
//
// The two rules worth commentRefFailScore each describe one of the two ways a reference
// rots: the symbol MOVED, so its name is still real under exactly one other qualifier, or
// it was RENAMED in place, so its successor sits in the package it names.
//
// Everything else that matched is scored lower and printed rather than enforced. That
// tier is the point of scoring instead of asserting: a lowercase near-miss is worth a
// reader's glance and is not worth a red gate, and deciding which it is needs judgement
// this test does not have.
//
// Both rules were MEASURED loose before they were narrowed, and what narrowed them is
// worth keeping: this is one module with thousands of symbols, so almost any short name
// exists somewhere. Bare `exists elsewhere` accused types.Run and spells.Project, where
// the name is declared in a dozen packages and proves nothing; UNIQUENESS is the part
// that carries the signal. Bare `within two edits` accused cache.immutable of being
// mutable, because two edits is ordinary English distance.
//
// Both then need the same second narrowing, for the same reason: a single exported word
// is a word everyone uses. yaml.Decoder is gopkg.in/yaml.v3's and json.Marshaler is the
// standard library's, and each was accused of belonging to the one local package that
// happens to declare that name. Demanding a COMPOSED name is what separates an identifier
// somebody wrote here from a word two modules were always going to share. It costs the
// single-word renames, which land in the printed tier instead.
//
// Absence from a known Buzz module is deliberately NOT a rule, though it sounds like the
// sharpest one available. MEASURED: it fired on yaml.v3 (a Go import path), std.buzz (a
// filename), vcs.changed_files (an MCP tool name) and magus.inputs (a magusfile key), all
// sharing one identifier space with the module surface. No surface here is knowably
// exhaustive, so "the module lacks it" cannot mean what it appears to.
func (ix symbolIndex) score(pkg, sym string) (int, string) {
	composed := isMultiwordExported(sym)
	if owner := ix.exported[sym]; composed && owner != "" && owner != pkg {
		return commentRefFailScore, "only package " + owner + " declares it, so the qualifier rotted"
	}
	if near, ok := ix.nearest(pkg, sym); ok {
		if composed {
			return commentRefFailScore, "the package declares " + near + ", close enough to be a rename"
		}
		return 3, "the package declares " + near + ", but a one-word near-miss is usually prose"
	}
	if strings.ToLower(sym) != sym {
		return 2, "reads like an identifier, but nothing in the module matches it"
	}
	return 0, ""
}

// externalImports names the packages a file binds from outside this module.
//
// A comment in that file citing one of those names means THAT package: yaml.Decoder in a
// file importing gopkg.in/yaml.v3 is yaml.v3's, however loudly a local package called
// yaml disagrees. Nothing here can check another module's symbols, so those references
// are not judged at all.
func externalImports(f *ast.File) map[string]bool {
	out := map[string]bool{}
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || strings.HasPrefix(path, "github.com/egladman/magus") {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		name, _, _ = strings.Cut(name, ".") // gopkg.in/yaml.v3 imports as yaml
		if imp.Name != nil {
			name = imp.Name.Name
		}
		out[name] = true
	}
	return out
}

// isMultiwordExported reports whether sym can only be a Go identifier: exported, and
// carrying a second word. One exported word (Merge, Marshaler) is also an English word;
// two (PatchDigest, TrackDependencyWait) is a name somebody composed.
func isMultiwordExported(sym string) bool {
	return ast.IsExported(sym) && strings.ToLower(sym[1:]) != sym[1:]
}

// nearest is a symbol in pkg close enough to sym to be its successor: the same name but
// for case, or within two edits. The length floor keeps short words out, where two edits
// reach most of the dictionary.
func (ix symbolIndex) nearest(pkg, sym string) (string, bool) {
	for cand := range ix.byPkg[pkg] {
		if strings.EqualFold(cand, sym) {
			return cand, true
		}
		if len(sym) >= 6 && len(cand) >= 6 && editDistance(cand, sym) <= 2 {
			return cand, true
		}
	}
	return "", false
}

// editDistance is Levenshtein over bytes, one row at a time.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(min(cur[j-1]+1, prev[j]+1), prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// buildSymbolIndex walks the tree once and returns the index plus the Go files to scan.
//
// Packages sharing a name have their symbols UNIONED, and each is indexed under its
// directory name as well. Both widen what resolves, which is the conservative direction:
// a union can only hide a rotted reference, never invent one, and this gate would rather
// miss than accuse.
func buildSymbolIndex(t *testing.T) (symbolIndex, []string) {
	t.Helper()
	ix := symbolIndex{
		byPkg:    map[string]map[string]bool{},
		exported: map[string]string{},
		host:     map[string]bool{},
		files:    map[string]bool{},
	}
	var files []string

	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable subtree is skipped, not fatal
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "worktrees", "gen", ".git", "vendor", "testdata", "dist":
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

// add records one file's declarations under both its package name and its directory.
func (ix symbolIndex) add(f *ast.File, dir string) {
	keys := []string{f.Name.Name}
	if dir != f.Name.Name {
		keys = append(keys, dir)
	}
	for _, k := range keys {
		if ix.byPkg[k] == nil {
			ix.byPkg[k] = map[string]bool{}
		}
	}
	declare := func(name string) {
		for _, k := range keys {
			ix.byPkg[k][name] = true
		}
		// A name several packages declare is recorded as ambiguous, because the qualifier
		// is only checkable against a symbol that has exactly one home. main is never a
		// home: every command declares Run, and none of them is THE Run.
		if ast.IsExported(name) && f.Name.Name != "main" {
			if owner, seen := ix.exported[name]; !seen {
				ix.exported[name] = f.Name.Name
			} else if owner != f.Name.Name {
				ix.exported[name] = ""
			}
		}
	}

	ast.Inspect(f, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			declare(node.Name.Name)
		case *ast.TypeSpec:
			declare(node.Name.Name)
		case *ast.ValueSpec:
			for _, name := range node.Names {
				declare(name.Name)
			}
		case *ast.Field:
			// Struct fields and interface methods. Prose cites config.TargetTimeout the
			// same way it cites a function, and the compiler is not what is reading it.
			for _, name := range node.Names {
				declare(name.Name)
			}
		case *ast.BasicLit:
			if node.Kind == token.STRING {
				if lit, err := strconv.Unquote(node.Value); err == nil {
					ix.addBuzzName(lit)
				}
			}
		}
		return true
	})
}

// addBuzzName records the host surface a string literal spells.
//
// Buzz is the other language a comment in this module cites, and the generated signature
// table (internal/langservice/manifest_data.go) writes every member of it. Reading those
// literals is magus's own answer to what Buzz exposes, rather than a guess assembled from
// registration calls.
//
// Comments cite these with a dot where source uses a backslash, so both spellings land in
// one index. A namespace member is cited by its tail alone (`magus\secret.read` reads as
// secret.read), which is why that form is recorded twice.
func (ix symbolIndex) addBuzzName(lit string) {
	if commentRefRe.FindString(lit) == lit {
		ix.host[lit] = true // a binding registered under its own dotted name
		return
	}
	for _, m := range buzzNameRe.FindAllStringSubmatch(lit, -1) {
		module, member, namespaced := m[1], m[2], m[3]
		ix.host[module+"."+member] = true
		if namespaced != "" {
			ix.host[member+namespaced] = true
		}
	}
}

// sockdirPackage is the one place magus resolves the per-user runtime directory, where
// the broker and the server listen.
const sockdirPackage = "github.com/egladman/magus/internal/proc/sockdir"

// TestTestBinariesNeverReachTheUserRuntimeDir keeps every test process off the machine it
// runs on. A test binary that links the socket directory can resolve the person's real
// one, and then Open dials their broker, claims capacity from it, and prints the
// not-arbitrated warning when none is up; a run binds its pool beside their server.
// libs/testkit points the process at a private directory and pins the broker off, and
// this holds every such binary to calling it from TestMain.
//
// The link graph is go list's, not a hand-kept list: the binaries that can reach the
// directory grow with every import, and a list would miss the next one.
func TestTestBinariesNeverReachTheUserRuntimeDir(t *testing.T) {
	t.Parallel()
	format := `{{if not .ForTest}}{{.ImportPath}}{{"\t"}}{{.Dir}}{{"\t"}}{{join .Deps " "}}{{end}}`
	out, err := exec.CommandContext(t.Context(), "go", "list", "-test", "-f", format, "./...").Output()
	require.NoError(t, err, "go list")

	checked := 0
	for line := range strings.Lines(string(out)) {
		path, rest, _ := strings.Cut(strings.TrimSpace(line), "\t")
		dir, deps, _ := strings.Cut(rest, "\t")
		if !strings.HasSuffix(path, ".test") || !slices.Contains(strings.Fields(deps), sockdirPackage) {
			continue
		}
		checked++
		assert.True(t, testMainIsolates(t, dir),
			"%s links %s but no TestMain in %s calls testkit.Main or testkit.Isolated, so its tests "+
				"can reach the person's real runtime directory: add `func TestMain(m *testing.M) { testkit.Main(m) }`",
			strings.TrimSuffix(path, ".test"), sockdirPackage, dir)
	}
	require.NotZero(t, checked, "no test binary links %s; this gate went quiet rather than red", sockdirPackage)
}

// testMainIsolates reports whether a TestMain among dir's test files calls testkit.Main
// or testkit.Isolated.
func testMainIsolates(t *testing.T, dir string) bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	require.NoError(t, err)
	for _, path := range files {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		require.NoError(t, err, "parsing %s", path)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name.Name != "TestMain" || fn.Body == nil {
				continue
			}
			found := false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok {
					if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "testkit" &&
						(sel.Sel.Name == "Main" || sel.Sel.Name == "Isolated") {
						found = true
					}
				}
				return !found
			})
			if found {
				return true
			}
		}
	}
	return false
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
var envRegistrySkipDirs = map[string]bool{
	".git": true, ".magus": true, ".claude": true, ".agents": true, ".opencode": true,
	"node_modules": true, "testdata": true,
}

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
	"cmd/magus-utils/", "tools/", ".github/", "benchmarks/", "hack/", "magusfile.buzz",
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
