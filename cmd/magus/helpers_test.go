package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/cache"
	internalmcp "github.com/egladman/magus/internal/handler/mcp"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
)

// TestStatusGlyph maps every documented status to its plain (uncoloured) marker and
// confirms the unknown-status fallback.
func TestStatusGlyph(t *testing.T) {
	assert.Equal(t, "[pass]", statusGlyph(types.DoctorOK, false))
	assert.Equal(t, "[fail]", statusGlyph(types.DoctorFail, false))
	assert.Equal(t, "[?]", statusGlyph("", false))
	assert.Equal(t, "[?]", statusGlyph("unknown", false))
	assert.Equal(t, "[?]", statusGlyph("OK", false)) // case-sensitive by design
	// Coloured variant wraps the marker in ANSI but preserves the label.
	assert.Contains(t, statusGlyph(types.DoctorFail, true), "[fail]")
	assert.Contains(t, statusGlyph(types.DoctorFail, true), "\x1b[31m")
}

// TestCanonicalTarget covers the short-alias expansions and the passthrough.
func TestCanonicalTarget(t *testing.T) {
	assert.Equal(t, "format", canonicalTarget("fmt"))
	assert.Equal(t, "generate", canonicalTarget("gen"))
	assert.Equal(t, "build", canonicalTarget("build"))
	assert.Equal(t, "", canonicalTarget(""))
}

// TestWithDefaultCharms covers the magus.yaml default_charms merge: defaults are
// applied to a run, per-run charms stack on top (exact dups dropped), and the
// --no-default-charms escape ignores the defaults.
func TestWithDefaultCharms(t *testing.T) {
	cases := []struct {
		name      string
		perRun    []string
		defaults  []string
		noDefault bool
		want      []string
	}{
		{"defaults applied to a bare run", nil, []string{"rw"}, false, []string{"rw"}},
		{"per-run stacks on defaults", []string{"debug"}, []string{"rw"}, false, []string{"rw", "debug"}},
		{"exact duplicate dropped", []string{"rw"}, []string{"rw"}, false, []string{"rw"}},
		{"no-default-charms ignores defaults", []string{"debug"}, []string{"rw"}, true, []string{"debug"}},
		{"no defaults is identity", []string{"debug"}, nil, false, []string{"debug"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, withDefaultCharms(c.perRun, c.defaults, c.noDefault))
		})
	}
}

// TestMergeDriverRefreshSuppression pins the guard that keeps the merge driver from
// rewriting the tracked .gitattributes. loadMagus refreshes the merge-driver registration
// on the memoizing path, and that write lands in the working tree - which, when the caller
// IS the merge driver running inside the VCS's index manipulation, is the dirty-tree
// failure that stops `git rebase --continue`. Every other caller must still refresh.
func TestMergeDriverRefreshSuppression(t *testing.T) {
	assert.False(t, skipMergeDriverRefresh(context.Background()),
		"an ordinary command must keep the merge-driver registration honest")
	assert.True(t, skipMergeDriverRefresh(withoutMergeDriverRefresh(context.Background())),
		"a load from inside the merge driver must not rewrite .gitattributes")
	// The marker must not leak backwards onto the parent context.
	parent := context.Background()
	_ = withoutMergeDriverRefresh(parent)
	assert.False(t, skipMergeDriverRefresh(parent), "suppression must not escape the derived context")
}

// TestSplitOnDashDash pins the forwarding boundary: everything after "--" is the spell's
// verbatim argv, and an absent separator must not manufacture an empty tail.
func TestSplitOnDashDash(t *testing.T) {
	before, after := splitOnDashDash([]string{"test", "web"})
	assert.Equal(t, []string{"test", "web"}, before)
	assert.Nil(t, after)

	before, after = splitOnDashDash([]string{"test", "--", "-run", "TestX"})
	assert.Equal(t, []string{"test"}, before)
	assert.Equal(t, []string{"-run", "TestX"}, after)

	// A trailing separator forwards an empty argv, which is different from forwarding none.
	before, after = splitOnDashDash([]string{"test", "--"})
	assert.Equal(t, []string{"test"}, before)
	assert.Equal(t, []string{}, after)
}

// TestSplitOnThen pins the two things the chain grammar exists to keep straight: "--" is
// split off FIRST so a spell can receive the literal word "--then", and an absent separator
// is distinguishable from one with nothing after it.
func TestSplitOnThen(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		before, after, found := splitOnThen([]string{"build", "web"})
		assert.False(t, found)
		assert.Equal(t, []string{"build", "web"}, before)
		assert.Nil(t, after)
	})

	t.Run("present with a verb", func(t *testing.T) {
		before, after, found := splitOnThen([]string{"build", "web", "--then", "outputs"})
		assert.True(t, found)
		assert.Equal(t, []string{"build", "web"}, before)
		assert.Equal(t, []string{"outputs"}, after)
	})

	t.Run("present with no verb is still found", func(t *testing.T) {
		before, after, found := splitOnThen([]string{"build", "--then"})
		assert.True(t, found)
		assert.Equal(t, []string{"build"}, before)
		assert.Empty(t, after)
	})

	t.Run("a forwarded --then belongs to the spell", func(t *testing.T) {
		before, after, found := splitOnThen([]string{"test", "--", "--then"})
		assert.False(t, found, "everything after -- is the spell's argv, not the chain grammar's")
		assert.Equal(t, []string{"test", "--", "--then"}, before)
		assert.Nil(t, after)
	})

	t.Run("the forwarded argv is re-attached", func(t *testing.T) {
		before, after, found := splitOnThen([]string{"test", "--then", "value", "--", "-run", "TestX"})
		assert.True(t, found)
		assert.Equal(t, []string{"test", "--", "-run", "TestX"}, before)
		assert.Equal(t, []string{"value"}, after)
	})
}

func TestListTargetsPrintsOnlyPaths(t *testing.T) {
	out := captureStdout(t, func() {
		listTargets("affected", []types.Target{{Path: "web"}, {Path: "api"}}, "vcs")
	})
	assert.Equal(t, "web\napi\n", out, "the summary is a log line; stdout carries only the paths")

	assert.Empty(t, captureStdout(t, func() { listTargets("all", nil, "") }))

	out = captureStdout(t, func() {
		listTargets("cwd", []types.Target{{Path: "web"}}, "")
	})
	assert.Equal(t, "web\n", out)
}

// TestReportedRunErr covers the gate that stops a fan-out failure being printed twice: the
// cache's handler already rendered a per-project line, so the top-level handler must exit
// silently rather than reprint it.
func TestReportedRunErr(t *testing.T) {
	assert.False(t, reportedRunErr(nil))
	assert.False(t, reportedRunErr(errors.New("plain")))
	assert.True(t, reportedRunErr(&types.SpellErrors{Project: "web"}))
	assert.True(t, reportedRunErr(errors.Join(errors.New("other"), &types.SpellErrors{Project: "web"})))
}

func TestErrSilentIsAlreadyReported(t *testing.T) {
	var e error = errSilent{exitCode: 2}
	assert.Equal(t, "silent exit", e.Error())

	// AlreadyReported is what a forwarded failure needs: across a socket the receiving
	// process cannot see the concrete type, and "silent exit" is a sentence about magus's
	// internals rather than about the failure.
	reported, ok := e.(interface{ AlreadyReported() bool })
	require.True(t, ok)
	assert.True(t, reported.AlreadyReported())
}

// TestCLIErrorsCarryTheirExitCode pins the method the DAEMON reads. exitCodeOf sees the
// concrete types and could go on reading the fields; a forwarded run cannot, so without
// the method `magus run bogus-target` exited 2 alone and 1 under a daemon.
func TestCLIErrorsCarryTheirExitCode(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{errSilent{exitCode: 2}, 2},
		{errSilent{exitCode: 1}, 1},
		{usagef("no such target"), exitUsage},
		{fmt.Errorf("wrapped: %w", errSilent{exitCode: 2}), 2},
	} {
		code, ok := proc.ExitCode(tc.err)
		require.True(t, ok, "%v must state its exit code", tc.err)
		assert.Equal(t, tc.want, code)
	}

	_, ok := proc.ExitCode(errors.New("the work failed"))
	assert.False(t, ok, "an ordinary failure names no code and stays the daemon's default 1")
}

// TestMachineBusyRidesTheExitCodeSeam pins that a machine-budget refusal needs no
// branch of its own in exitCodeOf. The local path and the daemon now ask the error the
// same question, so the refusal must answer it rather than be recognised by type or by
// diagnostic code - which is what lets one seam serve both this and a contended lock.
func TestMachineBusyRidesTheExitCodeSeam(t *testing.T) {
	// The refusal is built inside the cache, so stand in for it with an error carrying
	// the same two properties the real one does (machine_test.go pins that it does).
	busy := machineBusyStub{types.DiagnosticErrorf(types.MachineBudgetExhausted, "the machine is full")}

	code, ok := proc.ExitCode(busy)
	require.True(t, ok, "the daemon must be able to read the code off a forwarded refusal")
	assert.Equal(t, cache.ExitCodeMachineBusy, code)
	assert.Equal(t, cache.ExitCodeMachineBusy, exitCodeOf(busy), "and the local path must agree")
	assert.Equal(t, cache.ExitCodeMachineBusy, exitCodeOf(fmt.Errorf("run: %w", busy)),
		"through every layer between the step and main")

	// A run where real targets ALSO failed is a broken build, not a scheduling problem:
	// errSilent is matched first and keeps 1, so a peer being busy cannot rewrite the
	// verdict on work that actually ran.
	assert.Equal(t, 1, exitCodeOf(errors.Join(errSilent{exitCode: 1}, busy)))
}

type machineBusyStub struct{ error }

func (machineBusyStub) ExitCode() int { return cache.ExitCodeMachineBusy }

func (e machineBusyStub) Unwrap() error { return e.error }

func TestUsagefCarriesTheMisuseMessage(t *testing.T) {
	err := usagef("magus diff: %d is too many", 3)
	assert.EqualError(t, err, "magus diff: 3 is too many")
	assert.IsType(t, errUsage{}, err)
}

func TestFlagIsBool(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.Bool("force", false, "")
	fs.String("dir", "", "")

	assert.True(t, flagIsBool(fs.Lookup("force")))
	assert.False(t, flagIsBool(fs.Lookup("dir")))
}

// TestIsFlagNamedAndFlagValueOf pins all four spellings Go's flag package accepts. A
// hand-rolled scanner that handles three of them fails invisibly on the fourth, which is
// exactly how `magus affected -explain .` came to read "." as a target name.
func TestIsFlagNamedAndFlagValueOf(t *testing.T) {
	assert.True(t, isFlagNamed("-explain", "explain"))
	assert.True(t, isFlagNamed("--explain", "explain"))
	assert.False(t, isFlagNamed("-explain=web", "explain"))
	assert.False(t, isFlagNamed("--explains", "explain"))

	assert.Equal(t, "web", flagValueOf("-explain=web", "explain"))
	assert.Equal(t, "web", flagValueOf("--explain=web", "explain"))
	assert.Equal(t, "", flagValueOf("--explain", "explain"))
	assert.Equal(t, "", flagValueOf("--base=main", "explain"))
}

func TestOutputOptionsOrDefault(t *testing.T) {
	prev := global.output
	t.Cleanup(func() { global.output = prev })

	global.output = ""
	opts, err := outputOptionsOrDefault()
	require.NoError(t, err)
	assert.Equal(t, outputText, opts.Format)

	global.output = "json"
	opts, err = outputOptionsOrDefault()
	require.NoError(t, err)
	assert.Equal(t, outputJSON, opts.Format)

	global.output = "nonsense"
	_, err = outputOptionsOrDefault()
	assert.Error(t, err)
}

func TestHasModeFlagAcceptsEverySpelling(t *testing.T) {
	for _, arg := range []string{"--plan", "-plan", "--plan=3", "-plan=3"} {
		assert.True(t, hasModeFlag([]string{"ci", arg}, "plan"), arg)
	}
	assert.False(t, hasModeFlag([]string{"ci", "--planner"}, "plan"))
	assert.False(t, hasModeFlag(nil, "plan"))
}

func TestCountLabelAndImpactPct(t *testing.T) {
	assert.Equal(t, "0 files", countLabel(0, "file", "files"))
	assert.Equal(t, "1 file", countLabel(1, "file", "files"))
	assert.Equal(t, "3 files", countLabel(3, "file", "files"))

	assert.Equal(t, "0%", impactPct(0))
	assert.Equal(t, "25%", impactPct(0.25))
	assert.Equal(t, "76%", impactPct(0.756))
	assert.Equal(t, "100%", impactPct(1))
}

func TestPluralSuffixAndLinesSuffix(t *testing.T) {
	assert.Equal(t, "", pluralSuffix(1, "", "s"))
	assert.Equal(t, "s", pluralSuffix(0, "", "s"))
	assert.Equal(t, "s", pluralSuffix(2, "", "s"))

	assert.Equal(t, "", linesSuffix(nil))
	assert.Equal(t, "  lines 7", linesSuffix([]int{7}))
	assert.Equal(t, "  lines 7,19,42", linesSuffix([]int{7, 19, 42}))
}

func TestStrOrDefAndIntOrDef(t *testing.T) {
	assert.Equal(t, "(default)", strOrDef("", "(default)"))
	assert.Equal(t, "set", strOrDef("set", "(default)"))

	// Zero means "nothing was configured", not "zero builds may run", so it renders as
	// the placeholder rather than as the number.
	assert.Equal(t, "(default)", intOrDef(0, "(default)"))
	assert.Equal(t, "8", intOrDef(8, "(default)"))
}

func TestSplitConfigCommasHonorsEscapes(t *testing.T) {
	assert.Equal(t, []string{"a", "b", "c"}, splitConfigCommas("a,b,c"))
	assert.Equal(t, []string{"go,rust"}, splitConfigCommas(`go\,rust`))
	assert.Equal(t, []string{"key=a,b", "value=c"}, splitConfigCommas(`key=a\,b,value=c`))
	assert.Equal(t, []string{""}, splitConfigCommas(""))
	// A trailing backslash is literal, not an escape with nothing to escape.
	assert.Equal(t, []string{`a\`}, splitConfigCommas(`a\`))
}

func TestParseConfigSetArg(t *testing.T) {
	key, value, err := parseConfigSetArg("key=cache.mode,value=write")
	require.NoError(t, err)
	assert.Equal(t, "cache.mode", key)
	assert.Equal(t, "write", value)

	// The value keeps its own '=' and its escaped commas.
	key, value, err = parseConfigSetArg(`key=spells,value=go\,rust`)
	require.NoError(t, err)
	assert.Equal(t, "spells", key)
	assert.Equal(t, "go,rust", value)

	// A key with no value is legal: it is how a key is set to empty.
	key, value, err = parseConfigSetArg("key=cache.dir")
	require.NoError(t, err)
	assert.Equal(t, "cache.dir", key)
	assert.Equal(t, "", value)

	_, _, err = parseConfigSetArg("cache.mode")
	assert.ErrorContains(t, err, "not in key=value form")

	_, _, err = parseConfigSetArg("k=cache.mode")
	assert.ErrorContains(t, err, `unknown field "k"`)

	_, _, err = parseConfigSetArg("value=write")
	assert.ErrorContains(t, err, "key is required")
}

func TestFmtBytes(t *testing.T) {
	assert.Equal(t, "0 B", fmtBytes(0))
	assert.Equal(t, "512 B", fmtBytes(512))
	assert.Equal(t, "1.0 KiB", fmtBytes(1<<10))
	assert.Equal(t, "1.5 MiB", fmtBytes(3<<19))
	assert.Equal(t, "2.0 GiB", fmtBytes(2<<30))
}

func TestRoughAge(t *testing.T) {
	assert.Equal(t, "under an hour", roughAge(59*time.Minute))
	assert.Equal(t, "3h", roughAge(3*time.Hour))
	assert.Equal(t, "2d", roughAge(48*time.Hour))
}

func TestFirstLine(t *testing.T) {
	assert.Equal(t, "", firstLine(""))
	assert.Equal(t, "one", firstLine("one"))
	assert.Equal(t, "one", firstLine("one\ntwo\nthree"))
	assert.Equal(t, "", firstLine("\ntwo"))
}

func TestFilterByNameAndNamesOf(t *testing.T) {
	items := []types.SpellVersion{{Tool: "go"}, {Tool: "node"}}
	nameOf := func(v types.SpellVersion) string { return v.Tool }

	assert.Equal(t, []types.SpellVersion{{Tool: "node"}}, filterByName(items, "node", nameOf))
	assert.Nil(t, filterByName(items, "rustc", nameOf))
	assert.Equal(t, []string{"go", "node"}, namesOf(items, nameOf))
	assert.Empty(t, namesOf(nil, nameOf))
}

// TestUnknownEntitySuggestsAndExitsTwo pins both halves: a near miss earns a suggestion,
// and the caller gets the already-printed sentinel so the message is not doubled.
func TestUnknownEntitySuggestsAndExitsTwo(t *testing.T) {
	var err error
	out := captureStderr(t, func() {
		err = unknownEntity("spell", "gol", []string{"go", "typescript"})
	})

	assert.Equal(t, errSilent{exitCode: 2}, err)
	assert.Contains(t, out, `magus describe spell: unknown spell "gol"`)
	assert.Contains(t, out, `did you mean "go"?`)

	out = captureStderr(t, func() {
		err = unknownEntity("spell", "zzzzzzzz", []string{"go"})
	})
	assert.Equal(t, errSilent{exitCode: 2}, err)
	assert.NotContains(t, out, "did you mean")
}

// TestHashOfKeyInputsIsOrderSensitive guards the property a cache key needs: the same lines
// in a different order are a different key, because the join is what is hashed.
func TestHashOfKeyInputsIsOrderSensitive(t *testing.T) {
	a := hashOfKeyInputs([]string{"a", "b"})
	assert.Len(t, a, 64)
	assert.Equal(t, a, hashOfKeyInputs([]string{"a", "b"}))
	assert.NotEqual(t, a, hashOfKeyInputs([]string{"b", "a"}))
	assert.NotEqual(t, a, hashOfKeyInputs(nil))
}

// TestPrintSpellVersionsSeparatesFailureFromSilence pins the three readings a probe can
// have: a version, a tool that reported none, and a probe that failed. Collapsing any two
// would make a broken toolchain look like a quiet one.
func TestPrintSpellVersionsSeparatesFailureFromSilence(t *testing.T) {
	assert.Empty(t, captureStdout(t, func() { printSpellVersions(nil) }))

	out := captureStdout(t, func() {
		printSpellVersions([]types.SpellVersion{
			{Tool: "go", Version: "go1.25.0", CacheKey: "abc123"},
			{Tool: "node", Error: "executable file not found"},
			{Tool: "tsc"},
		})
	})

	assert.Contains(t, out, "versions (probed just now, never cached)")
	assert.Contains(t, out, "go1.25.0")
	assert.Contains(t, out, "key: abc123")
	assert.Contains(t, out, "probe failed: executable file not found")
	assert.Contains(t, out, "(no version reported)")
}

// TestTargetSummaryPrefersTheAuthorsWords pins the fallback order: a doc comment beats a
// derived description, and a target that composes only siblings still says something.
func TestTargetSummaryPrefersTheAuthorsWords(t *testing.T) {
	assert.Equal(t, "build the site", targetSummary(types.TargetGraphNode{
		Doc:    "build the site",
		Spells: []types.TargetSpellUse{{Spell: "go", Ops: []string{"go-build"}}},
	}))

	assert.Equal(t, "[go: go-build, go-vet; md: lint]", targetSummary(types.TargetGraphNode{
		Spells: []types.TargetSpellUse{
			{Spell: "go", Ops: []string{"go-build", "go-vet"}},
			{Spell: "md", Ops: []string{"lint"}},
		},
	}))

	assert.Equal(t, "[needs: build, test]", targetSummary(types.TargetGraphNode{
		Dependencies: []string{"build", "test"},
	}))

	assert.Equal(t, "", targetSummary(types.TargetGraphNode{Name: "bare"}))
}

// TestParseGitRemote covers both remote spellings a forge hands out, plus the refusals that
// keep a wrong link from being emitted.
func TestParseGitRemote(t *testing.T) {
	tests := []struct {
		remote            string
		host, owner, repo string
	}{
		{"git@github.com:egladman/magus.git", "github.com", "egladman", "magus"},
		{"https://github.com/egladman/magus.git", "github.com", "egladman", "magus"},
		{"https://user@git.example.com:8443/team/repo", "git.example.com", "team", "repo"},
		{"ssh://git@github.com/egladman/magus", "github.com", "egladman", "magus"},
		{"  git@github.com:egladman/magus  ", "github.com", "egladman", "magus"},
		{"github.com/egladman/magus", "", "", ""},
		{"git@github.com", "", "", ""},
		{"https://github.com/egladman", "github.com", "", ""},
	}

	for _, tc := range tests {
		host, owner, repo := parseGitRemote(tc.remote)
		assert.Equal(t, tc.host, host, tc.remote)
		assert.Equal(t, tc.owner, owner, tc.remote)
		assert.Equal(t, tc.repo, repo, tc.remote)
	}
}

// TestGithubRawBase pins the deliberate narrowness: other forges serve raw content from
// different hosts, so an unmapped one yields no link rather than a wrong one.
func TestGithubRawBase(t *testing.T) {
	assert.Equal(t,
		"https://raw.githubusercontent.com/egladman/magus/main",
		githubRawBase("https://github.com/egladman/magus/blob/main"))

	assert.Equal(t, "", githubRawBase("https://gitlab.com/o/r/-/blob/main"))
	assert.Equal(t, "", githubRawBase(""))
}

// TestGraphExplorerLinkNeedsACommittedGraph covers the guard that keeps MAGUS.md from
// carrying a link that 404s. No docs/graph.json means no link, decided before any VCS
// resolution is attempted.
func TestGraphExplorerLinkNeedsACommittedGraph(t *testing.T) {
	assert.Equal(t, "", graphExplorerLink(t.Context(), t.TempDir()))
}

func TestJoinFormats(t *testing.T) {
	assert.Equal(t, "text|json|yaml|jsonl|name", JoinFormats(CommonFormats, "|"))
	assert.Equal(t, "json, yaml", JoinFormats([]Format{FormatJSON, FormatYAML}, ", "))
	assert.Equal(t, "", JoinFormats(nil, ","))
}

func TestWriteYAMLIndentsTwo(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, writeYAML(&buf, map[string]any{"outer": map[string]any{"inner": 1}}))
	assert.Equal(t, "outer:\n  inner: 1\n", buf.String())
}

// TestTypeLabelDropsPackageQualifiers pins the -o template field listing's vocabulary: it
// reads like the json shape, not like Go's fully-qualified type names.
func TestTypeLabelDropsPackageQualifiers(t *testing.T) {
	type row struct {
		Ref   types.ProjectRef
		Refs  []types.ProjectRef
		Ptr   *types.ProjectRef
		Table map[string][]int
		Any   any
		Err   error
	}
	rt := reflect.TypeOf(row{})
	labelOf := func(name string) string {
		f, ok := rt.FieldByName(name)
		require.True(t, ok)
		return typeLabel(f.Type)
	}

	assert.Equal(t, "ProjectRef", labelOf("Ref"))
	assert.Equal(t, "[]ProjectRef", labelOf("Refs"))
	assert.Equal(t, "*ProjectRef", labelOf("Ptr"))
	assert.Equal(t, "map[string][]int", labelOf("Table"))
	assert.Equal(t, "any", labelOf("Any"))
	assert.Equal(t, "error", labelOf("Err"))
	assert.Equal(t, "[]string", typeLabel(reflect.TypeOf([]string{})))
}

func TestJSONShapeMirrorsTheWireNames(t *testing.T) {
	shaped, err := jsonShape(types.ProjectRef{Path: "web", Name: "web"})
	require.NoError(t, err)

	m, ok := shaped.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "web", m["path"])
	// Dir is json:"-": the template surface must not expose a host-absolute path.
	assert.NotContains(t, m, "dir")

	_, err = jsonShape(make(chan int))
	assert.Error(t, err)
}

// TestTemplateFuncsExcludesTheNonHermetic pins the curated sprig subset: a build-tool
// template must not read the clock, the environment, or a crypto source, because any of
// them makes an otherwise cacheable render non-reproducible.
func TestTemplateFuncsExcludesTheNonHermetic(t *testing.T) {
	f := templateFuncs()

	for _, name := range excludedSprigFuncs {
		assert.NotContains(t, f, name)
	}
	for _, name := range []string{"env", "expandenv", "getHostByName"} {
		assert.NotContains(t, f, name)
	}
	for _, name := range []string{"join", "upper", "lower", "trim", "quote", "b64enc"} {
		assert.Contains(t, f, name)
	}
}

func TestEmitFormattedWritesToStdout(t *testing.T) {
	prev := global.tee
	t.Cleanup(func() { global.tee = prev })
	global.tee = ""

	out := captureStdout(t, func() {
		require.NoError(t, emitFormatted(OutputOptions{Format: outputYAML}, types.ProjectRef{Path: "web", Name: "web"}))
	})
	assert.Contains(t, out, "path: web")

	out = captureStdout(t, func() {
		require.NoError(t, emitFormatted(OutputOptions{Format: outputTemplate, Template: "{{.path}}"}, types.ProjectRef{Path: "web"}))
	})
	assert.Equal(t, "web", out)
}

// TestMCPAddrFallsBackToTheDefault covers both arms of the address resolution. The
// unconfigured case has to yield a parseable host:port on its own, because that is what
// the doctor bridge check probes when nothing declares an address.
func TestMCPAddrFallsBackToTheDefault(t *testing.T) {
	prev := globalCfg.MCP.Address
	t.Cleanup(func() { globalCfg.MCP.Address = prev })

	globalCfg.MCP.Address = ""
	assert.Equal(t, internalmcp.DefaultAddress, mcpAddrString())
	parsed, err := mcpAddrPort()
	require.NoError(t, err)
	assert.NotZero(t, parsed.Port())

	globalCfg.MCP.Address = "127.0.0.1:9999"
	assert.Equal(t, "127.0.0.1:9999", mcpAddrString())
	parsed, err = mcpAddrPort()
	require.NoError(t, err)
	assert.Equal(t, uint16(9999), parsed.Port())
}

func TestDaemonDefaultAddrIsAUnixSocket(t *testing.T) {
	addr := daemonDefaultAddr()
	assert.True(t, strings.HasPrefix(addr, "unix://"), addr)
	assert.True(t, strings.HasSuffix(addr, "magus-daemon.sock"), addr)
}
