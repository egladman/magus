package main

import (
	"bytes"
	"go/format"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoIdent(t *testing.T) {
	cases := []struct{ in, want string }{
		{"no-default-charms", "NoDefaultCharms"},
		{"graph deps", "GraphDeps"},
		{"no-cache", "NoCache"},
		{"", ""},
		// The initialism table, which is the half no splitting algorithm can derive.
		{"url", "URL"},
		{"vcs", "VCS"},
		{"id", "ID"},
		{"api", "API"},
		{"ci", "CI"},
		{"json", "JSON"},
		{"yaml", "YAML"},
		{"tty", "TTY"},
		{"mcp", "MCP"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, goIdent(c.in), "goIdent(%q)", c.in)
	}
}

// TestGoIdentCollapsesCaseOnlyFlags pins the collision constsFor's panic exists to
// catch: -c and -C are two flags that reduce to one identifier fragment.
func TestGoIdentCollapsesCaseOnlyFlags(t *testing.T) {
	assert.Equal(t, goIdent("c"), goIdent("C"))
}

func TestDashesPicksTheSpellingAFlagIsTypedWith(t *testing.T) {
	assert.Equal(t, "-", dashes("c"))
	assert.Equal(t, "--", dashes("watch"))
	assert.Equal(t, "--", dashes(""))
}

func TestAliasGroupSpellings(t *testing.T) {
	g := aliasGroup{names: []string{"watch", "W"}}
	assert.Equal(t, "--watch, -W", g.spellings())
}

func TestGroupAliasesFoldsAnAliasIntoItsPrimary(t *testing.T) {
	groups := groupAliases("demo", []cli.Flag{
		{Name: "explain", Kind: cli.FlagBool},
		{Name: "e", Kind: cli.FlagBool, AliasOf: "explain"},
		{Name: "base", Kind: cli.FlagString},
	})

	require.Len(t, groups, 2, "an alias must not introduce a group of its own")
	assert.Equal(t, []string{"explain", "e"}, groups[0].names)
	assert.Equal(t, "Explain", groups[0].field)
	assert.Equal(t, []string{"base"}, groups[1].names)
	assert.Equal(t, "Base", groups[1].field)
}

// TestGroupAliasesNamesTheFieldForTheLongestSpelling covers the {t, test} case from the
// doc comment: naming the field after the PRIMARY would hand every read site f.T.
func TestGroupAliasesNamesTheFieldForTheLongestSpelling(t *testing.T) {
	groups := groupAliases("demo", []cli.Flag{
		{Name: "t", Kind: cli.FlagBool},
		{Name: "test", Kind: cli.FlagBool, AliasOf: "t"},
	})
	require.Len(t, groups, 1)
	assert.Equal(t, "Test", groups[0].field)
}

// TestGroupAliasesPanics pins all three rejections. Each would otherwise emit a
// shorthand that parses and writes to nothing, which is the bug AliasOf was added to
// prevent - so a silent extra field is the wrong outcome for every one of them.
func TestGroupAliasesPanics(t *testing.T) {
	t.Run("dangling AliasOf", func(t *testing.T) {
		assert.PanicsWithValue(t,
			`cliflags: demo --x is AliasOf "nope", which it does not declare`,
			func() {
				groupAliases("demo", []cli.Flag{
					{Name: "x", Kind: cli.FlagBool, AliasOf: "nope"},
				})
			})
	})

	t.Run("alias of an alias", func(t *testing.T) {
		assert.PanicsWithValue(t,
			"cliflags: demo --z aliases --y, which is itself an alias",
			func() {
				groupAliases("demo", []cli.Flag{
					{Name: "a", Kind: cli.FlagBool},
					{Name: "y", Kind: cli.FlagBool, AliasOf: "a"},
					{Name: "z", Kind: cli.FlagBool, AliasOf: "y"},
				})
			})
	})

	t.Run("kind mismatch", func(t *testing.T) {
		assert.PanicsWithValue(t,
			"cliflags: demo --b is string but aliases --a, which is bool",
			func() {
				groupAliases("demo", []cli.Flag{
					{Name: "a", Kind: cli.FlagBool},
					{Name: "b", Kind: cli.FlagString, AliasOf: "a"},
				})
			})
	})
}

func TestModesOfListsTheBaseFirst(t *testing.T) {
	flags := []cli.Flag{
		{Name: "no-cache", Kind: cli.FlagBool},
		{Name: "detail", Kind: cli.FlagBool, Modes: []string{"plan"}},
		{Name: "base", Kind: cli.FlagString, Modes: []string{"plan", "impact"}},
	}
	assert.Equal(t, []string{"", "plan", "impact"}, modesOf(flags))

	// A command declaring no modes keeps a single binder.
	assert.Equal(t, []string{""}, modesOf([]cli.Flag{{Name: "x", Kind: cli.FlagBool}}))
}

func TestFlagsForMode(t *testing.T) {
	flags := []cli.Flag{
		{Name: "no-cache", Kind: cli.FlagBool},
		{Name: "detail", Kind: cli.FlagBool, Modes: []string{"plan"}},
		{Name: "base", Kind: cli.FlagString, Modes: []string{"plan", "impact"}},
	}

	names := func(fs []cli.Flag) []string {
		out := make([]string, 0, len(fs))
		for _, f := range fs {
			out = append(out, f.Name)
		}
		return out
	}

	// The base takes the mode-less flags and nothing else: a mode is not "base plus
	// extras", it binds its own set.
	assert.Equal(t, []string{"no-cache"}, names(flagsForMode(flags, "")))
	assert.Equal(t, []string{"detail", "base"}, names(flagsForMode(flags, "plan")))
	assert.Equal(t, []string{"base"}, names(flagsForMode(flags, "impact")))
	assert.Empty(t, flagsForMode(flags, "bisect"))
}

func TestDocFor(t *testing.T) {
	flags := []cli.Flag{{Name: "watch", Kind: cli.FlagBool, Doc: "Watch it"}}
	assert.Equal(t, "Watch it", docFor(flags, "watch"))
	assert.Equal(t, "", docFor(flags, "absent"))
}

func TestGoTypeAndBindMethodCoverEveryBoundKind(t *testing.T) {
	cases := []struct {
		kind       cli.FlagKind
		goType     string
		bindMethod string
	}{
		{cli.FlagBool, "bool", "Bool"},
		{cli.FlagInt, "int", "Int"},
		{cli.FlagDuration, "time.Duration", "Duration"},
		{cli.FlagString, "string", "String"},
	}
	for _, c := range cases {
		assert.Equal(t, c.goType, goType(c.kind))
		assert.Equal(t, c.bindMethod, bindMethod(c.kind))
	}

	// FlagCustom is declared but never bound, so reaching either of these with it
	// means writeBinder's filter stopped working.
	assert.Panics(t, func() { goType(cli.FlagCustom) })
	assert.Panics(t, func() { bindMethod(cli.FlagCustom) })
}

func TestFlagDefaultLiteral(t *testing.T) {
	cases := []struct {
		name string
		flag cli.Flag
		want string
	}{
		{"bool true", cli.Flag{Kind: cli.FlagBool, Default: true}, "true"},
		{"bool false", cli.Flag{Kind: cli.FlagBool, Default: false}, "false"},
		{"bool nil default", cli.Flag{Kind: cli.FlagBool}, "false"},
		{"int", cli.Flag{Kind: cli.FlagInt, Default: 8}, "8"},
		{"int nil default", cli.Flag{Kind: cli.FlagInt}, "0"},
		// Rendered as the nanosecond count rather than "5s": the generated file is Go
		// source, and a duration literal has no untyped spelling.
		{"duration", cli.Flag{Kind: cli.FlagDuration, Default: 5 * time.Second}, "time.Duration(5000000000)"},
		{"duration nil default", cli.Flag{Kind: cli.FlagDuration}, "0"},
		{"string", cli.Flag{Kind: cli.FlagString, Default: "test"}, `"test"`},
		{"string nil default", cli.Flag{Kind: cli.FlagString}, `""`},
		// A Default of the wrong Go type falls back to the kind's zero rather than
		// emitting something unparsable.
		{"mistyped default", cli.Flag{Kind: cli.FlagInt, Default: "8"}, "0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, flagDefaultLiteral(c.flag))
		})
	}

	assert.Panics(t, func() { flagDefaultLiteral(cli.Flag{Kind: cli.FlagCustom}) })
}

func TestConstsFor(t *testing.T) {
	seen := map[string]string{}
	got := constsFor("demo", []cli.Flag{
		{Name: "watch", Kind: cli.FlagBool},
		{Name: "no-cache", Kind: cli.FlagBool},
	}, seen)

	require.Equal(t, []string{
		"\t// demo: --watch\n\tFlagDemoWatch = \"watch\"\n",
		"\t// demo: --no-cache\n\tFlagDemoNoCache = \"no-cache\"\n",
	}, got)

	// A command visited twice must not emit the constant twice: the second run is a
	// duplicate declaration, not a collision.
	again := constsFor("demo", []cli.Flag{{Name: "watch", Kind: cli.FlagBool}}, seen)
	assert.Empty(t, again)
}

// TestConstsForRefusesTwoFlagsUnderOneName is the collision goIdent's case-folding
// makes reachable: -c and -C are different flags and one constant.
func TestConstsForRefusesTwoFlagsUnderOneName(t *testing.T) {
	assert.PanicsWithValue(t,
		`cliflags: FlagDemoC would name both "c" and "C"`,
		func() {
			constsFor("demo", []cli.Flag{
				{Name: "c", Kind: cli.FlagBool},
				{Name: "C", Kind: cli.FlagBool},
			}, map[string]string{})
		})
}

// TestWalkCommandsDescendsEveryLevel is the one-level walk that left `config cache
// prune --older-than` and six siblings unbindable.
func TestWalkCommandsDescendsEveryLevel(t *testing.T) {
	root := cli.Command{
		Name: "config",
		Children: []cli.Command{{
			Name: "mcp",
			Children: []cli.Command{{
				Name:     "connector",
				Children: []cli.Command{{Name: "create"}},
			}},
		}},
	}

	var paths []string
	walkCommands(root.Name, root, func(path string, _ cli.Command) {
		paths = append(paths, path)
	})

	assert.Equal(t, []string{
		"config",
		"config mcp",
		"config mcp connector",
		"config mcp connector create",
	}, paths)
}

func TestWriteBinderEmitsTheStructAndOneBindPerSpelling(t *testing.T) {
	var b bytes.Buffer
	writeBinder(&b, "demo", "demo", []cli.Flag{
		{Name: "watch", Kind: cli.FlagBool, Doc: "Watch it"},
		{Name: "W", Kind: cli.FlagBool, AliasOf: "watch", Doc: "Short for --watch"},
	})
	got := b.String()

	assert.Contains(t, got, "// DemoFlags are the flags declared for `magus demo`.")
	assert.Contains(t, got, "type DemoFlags struct {")
	assert.Contains(t, got, "\tWatch bool // --watch, -W")
	assert.Contains(t, got, "func BindDemo(fs *flag.FlagSet) *DemoFlags {")
	// Both spellings write to the one destination; dropping either leaves a flag that
	// parses and does nothing.
	assert.Contains(t, got, `fs.BoolVar(&f.Watch, FlagDemoWatch, false, "Watch it")`)
	assert.Contains(t, got, `fs.BoolVar(&f.Watch, FlagDemoW, false, "Short for --watch")`)
	assert.Contains(t, got, "\treturn &f\n}\n")
	assert.NotContains(t, got, "DemoDefaults", "no flag declared DefaultAtBind")
}

// TestWriteBinderEmitsDefaultsForARuntimeDefault covers the shape that stopped
// generated binders from hardcoding a number the live binding disagreed with.
func TestWriteBinderEmitsDefaultsForARuntimeDefault(t *testing.T) {
	var b bytes.Buffer
	writeBinder(&b, "affected plan", "affected", []cli.Flag{
		{Name: "max-shards", Kind: cli.FlagInt, Default: 8, DefaultAtBind: true, Doc: "Maximum CI shards"},
		{Name: "detail", Kind: cli.FlagBool, Doc: "Per-shard detail"},
	})
	got := b.String()

	assert.Contains(t, got, "type AffectedPlanDefaults struct {")
	assert.Contains(t, got, "\tMaxShards int // --max-shards")
	assert.Contains(t, got, "func BindAffectedPlan(fs *flag.FlagSet, d AffectedPlanDefaults) *AffectedPlanFlags {")
	// The declared Default (8) documents the man page; the binder must take the
	// caller's value instead.
	assert.Contains(t, got, `fs.IntVar(&f.MaxShards, FlagAffectedMaxShards, d.MaxShards, "Maximum CI shards")`)
	assert.NotContains(t, got, "d.Detail", "only a DefaultAtBind flag reads from Defaults")
	// The mode binds a subset of ONE command's flags, so the constants stay the
	// command's rather than the mode's.
	assert.Contains(t, got, "FlagAffectedDetail")
	assert.NotContains(t, got, "FlagAffectedPlanDetail")
}

func TestWriteBinderEmitsNothingWithoutABindableFlag(t *testing.T) {
	t.Run("no flags", func(t *testing.T) {
		var b bytes.Buffer
		writeBinder(&b, "demo", "demo", nil)
		assert.Empty(t, b.String())
	})

	// A custom-valued flag is bound by the command itself; a second fs.Var for it
	// would panic at parse time with "flag redefined".
	t.Run("only custom flags", func(t *testing.T) {
		var b bytes.Buffer
		writeBinder(&b, "demo", "demo", []cli.Flag{{Name: "ignore", Kind: cli.FlagCustom}})
		assert.Empty(t, b.String())
	})
}

// TestWriteBinderEmitsParsableGo is the property `emit.Go` enforces at generate time,
// asked of one command instead of the whole registry.
func TestWriteBinderEmitsParsableGo(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("package gen\n\nimport (\n\t\"flag\"\n\t\"time\"\n)\n\n")
	b.WriteString("const (\n\tFlagDemoTimeout = \"timeout\"\n\tFlagDemoName = \"name\"\n\tFlagDemoDepth = \"depth\"\n)\n")
	writeBinder(&b, "demo", "demo", []cli.Flag{
		{Name: "timeout", Kind: cli.FlagDuration, Default: 90 * time.Second, Doc: "Give up after this long"},
		{Name: "name", Kind: cli.FlagString, Default: "main", Doc: "Which one"},
		{Name: "depth", Kind: cli.FlagInt, Doc: "How deep"},
	})

	_, err := format.Source(b.Bytes())
	require.NoError(t, err, "generated binder does not parse:\n%s", b.String())
}
