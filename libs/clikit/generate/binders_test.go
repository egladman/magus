package generate

import (
	"bytes"
	"go/format"
	"testing"
	"time"

	"github.com/egladman/magus/libs/clikit/command"
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
		equal(t, c.want, goIdent(c.in), "goIdent(%q)", c.in)
	}
}

// TestGoIdentCollapsesCaseOnlyFlags pins the collision renderConsts's panic exists to
// catch: -c and -C are two flags that reduce to one identifier fragment.
func TestGoIdentCollapsesCaseOnlyFlags(t *testing.T) {
	equal(t, goIdent("c"), goIdent("C"))
}

func TestDashesPicksTheSpellingAFlagIsTypedWith(t *testing.T) {
	equal(t, "-", dashes("c"))
	equal(t, "--", dashes("watch"))
	equal(t, "--", dashes(""))
}

func TestAliasGroupSpellings(t *testing.T) {
	g := aliasGroup{names: []string{"watch", "W"}}
	equal(t, "--watch, -W", g.spellings())
}

func TestGroupAliasesFoldsAnAliasIntoItsPrimary(t *testing.T) {
	explain := command.Flag{Name: "explain", Kind: command.Bool}
	base := command.Flag{Name: "base", Kind: command.String}
	groups := groupAliases("demo", []command.Flag{
		explain,
		{Name: "e", Kind: command.Bool, AliasOf: "explain"},
		base,
	})

	equal(t, 2, len(groups), "an alias must not introduce a group of its own")
	equal(t, []aliasGroup{
		{primary: explain, names: []string{"explain", "e"}, field: "Explain"},
		{primary: base, names: []string{"base"}, field: "Base"},
	}, groups)
}

// TestGroupAliasesNamesTheFieldForTheLongestSpelling covers the {t, test} case from the
// doc comment: naming the field after the PRIMARY would hand every read site f.T.
func TestGroupAliasesNamesTheFieldForTheLongestSpelling(t *testing.T) {
	groups := groupAliases("demo", []command.Flag{
		{Name: "t", Kind: command.Bool},
		{Name: "test", Kind: command.Bool, AliasOf: "t"},
	})
	equal(t, 1, len(groups))
	equal(t, "Test", groups[0].field)
}

// TestGroupAliasesPanics pins all three rejections. Each would otherwise emit a
// shorthand that parses and writes to nothing, which is the bug AliasOf was added to
// prevent, so a silent extra field is the wrong outcome for every one of them.
func TestGroupAliasesPanics(t *testing.T) {
	t.Run("dangling AliasOf", func(t *testing.T) {
		panicsWith(t,
			`binders: demo --x is AliasOf "nope", which it does not declare`,
			func() {
				groupAliases("demo", []command.Flag{
					{Name: "x", Kind: command.Bool, AliasOf: "nope"},
				})
			})
	})

	t.Run("alias of an alias", func(t *testing.T) {
		panicsWith(t,
			"binders: demo --z aliases --y, which is itself an alias",
			func() {
				groupAliases("demo", []command.Flag{
					{Name: "a", Kind: command.Bool},
					{Name: "y", Kind: command.Bool, AliasOf: "a"},
					{Name: "z", Kind: command.Bool, AliasOf: "y"},
				})
			})
	})

	t.Run("kind mismatch", func(t *testing.T) {
		panicsWith(t,
			"binders: demo --b is string but aliases --a, which is bool",
			func() {
				groupAliases("demo", []command.Flag{
					{Name: "a", Kind: command.Bool},
					{Name: "b", Kind: command.String, AliasOf: "a"},
				})
			})
	})
}

func TestModesOfListsTheBaseFirst(t *testing.T) {
	flags := []command.Flag{
		{Name: "no-cache", Kind: command.Bool},
		{Name: "detail", Kind: command.Bool, Modes: []string{"plan"}},
		{Name: "base", Kind: command.String, Modes: []string{"plan", "impact"}},
	}
	equal(t, []string{"", "plan", "impact"}, flagModes(flags))

	// A command declaring no modes keeps a single binder.
	equal(t, []string{""}, flagModes([]command.Flag{{Name: "x", Kind: command.Bool}}))
}

func TestFlagsForMode(t *testing.T) {
	flags := []command.Flag{
		{Name: "no-cache", Kind: command.Bool},
		{Name: "detail", Kind: command.Bool, Modes: []string{"plan"}},
		{Name: "base", Kind: command.String, Modes: []string{"plan", "impact"}},
	}

	names := func(fs []command.Flag) []string {
		out := make([]string, 0, len(fs))
		for _, f := range fs {
			out = append(out, f.Name)
		}
		return out
	}

	// The base takes the mode-less flags and nothing else: a mode is not "base plus
	// extras", it binds its own set.
	equal(t, []string{"no-cache"}, names(flagsForMode(flags, "")))
	equal(t, []string{"detail", "base"}, names(flagsForMode(flags, "plan")))
	equal(t, []string{"base"}, names(flagsForMode(flags, "impact")))
	empty(t, flagsForMode(flags, "bisect"))
}

func TestFlagDoc(t *testing.T) {
	flags := []command.Flag{{Name: "watch", Kind: command.Bool, Doc: "Watch it"}}
	equal(t, "Watch it", flagDoc(flags, "watch"))
	equal(t, "", flagDoc(flags, "absent"))
}

func TestGoTypeAndBindMethodCoverEveryBoundKind(t *testing.T) {
	cases := []struct {
		kind       command.Kind
		goType     string
		bindMethod string
	}{
		{command.Bool, "bool", "Bool"},
		{command.Int, "int", "Int"},
		{command.Duration, "time.Duration", "Duration"},
		{command.String, "string", "String"},
	}
	for _, c := range cases {
		equal(t, c.goType, goType(c.kind))
		equal(t, c.bindMethod, bindMethod(c.kind))
	}
	equal(t, "[]string", goType(command.StringList))

	// FlagCustom is declared but never bound, so reaching either of these with it
	// means writeBinder's filter stopped working.
	panics(t, func() { goType(command.Custom) })
	panics(t, func() { bindMethod(command.Custom) })
}

func TestFlagDefaultLiteral(t *testing.T) {
	cases := []struct {
		name string
		flag command.Flag
		want string
	}{
		{"bool true", command.Flag{Kind: command.Bool, Default: true}, "true"},
		{"bool false", command.Flag{Kind: command.Bool, Default: false}, "false"},
		{"bool nil default", command.Flag{Kind: command.Bool}, "false"},
		{"int", command.Flag{Kind: command.Int, Default: 8}, "8"},
		{"int nil default", command.Flag{Kind: command.Int}, "0"},
		// Rendered as the nanosecond count rather than "5s": the generated file is Go
		// source, and a duration literal has no untyped spelling.
		{"duration", command.Flag{Kind: command.Duration, Default: 5 * time.Second}, "time.Duration(5000000000)"},
		{"duration nil default", command.Flag{Kind: command.Duration}, "0"},
		{"string", command.Flag{Kind: command.String, Default: "test"}, `"test"`},
		{"string nil default", command.Flag{Kind: command.String}, `""`},
		{"string list", command.Flag{Kind: command.StringList}, "nil"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			equal(t, c.want, flagDefaultLiteral(c.flag))
		})
	}

	panics(t, func() { flagDefaultLiteral(command.Flag{Kind: command.Custom}) })
	panics(t, func() { flagDefaultLiteral(command.Flag{Name: "n", Kind: command.Int, Default: "8"}) })
}

func TestRenderConsts(t *testing.T) {
	seen := map[string]string{}
	got := renderConsts("demo", []command.Flag{
		{Name: "watch", Kind: command.Bool},
		{Name: "no-cache", Kind: command.Bool},
	}, seen)

	equal(t, []string{
		"\t// demo: --watch\n\tFlagDemoWatch = \"watch\"\n",
		"\t// demo: --no-cache\n\tFlagDemoNoCache = \"no-cache\"\n",
	}, got)

	// A command visited twice must not emit the constant twice: the second run is a
	// duplicate declaration, not a collision.
	again := renderConsts("demo", []command.Flag{{Name: "watch", Kind: command.Bool}}, seen)
	empty(t, again)
}

// TestRenderConstsRefusesTwoFlagsUnderOneName is the collision goIdent's case-folding
// makes reachable: -c and -C are different flags and one constant.
func TestRenderConstsRefusesTwoFlagsUnderOneName(t *testing.T) {
	panicsWith(t,
		`binders: FlagDemoC would name both "c" and "C"`,
		func() {
			renderConsts("demo", []command.Flag{
				{Name: "c", Kind: command.Bool},
				{Name: "C", Kind: command.Bool},
			}, map[string]string{})
		})
}

func TestWriteBinderEmitsTheStructAndOneBindPerSpelling(t *testing.T) {
	var b bytes.Buffer
	writeBinder(&b, "demo", "demo", []command.Flag{
		{Name: "watch", Kind: command.Bool, Default: false, Doc: "Watch it"},
		{Name: "W", Kind: command.Bool, AliasOf: "watch", Doc: "Short for --watch"},
	})
	got := b.String()

	contains(t, got, "// DemoFlags are the flags declared for `magus demo`.")
	contains(t, got, "type DemoFlags struct {")
	contains(t, got, "\tWatch bool // --[no-]watch, -W")
	contains(t, got, "func BindDemo(fs *flag.FlagSet) *DemoFlags {")
	// Both spellings write to the one destination; dropping either leaves a flag that
	// parses and does nothing.
	contains(t, got, `fs.BoolVar(&f.Watch, FlagDemoWatch, false, "Watch it")`)
	contains(t, got, "command.NegatedBoolVar(fs, &f.Watch, FlagDemoWatch)")
	notContains(t, got, "FlagDemoNoWatch", "the negation is derived, never a constant")
	notContains(t, got, "NegatedBoolVar(fs, &f.Watch, FlagDemoW)", "an alias gets no --no- form")
	contains(t, got, `fs.BoolVar(&f.Watch, FlagDemoW, false, "Short for --watch")`)
	contains(t, got, "\treturn &f\n}\n")
	notContains(t, got, "DemoDefaults", "no flag declared DefaultAtBind")
}

func TestWriteBinderBindsAStringListWithItsVar(t *testing.T) {
	var b bytes.Buffer
	writeBinder(&b, "demo", "demo", []command.Flag{
		{Name: "add-path", Kind: command.StringList, Doc: "A path to add; repeatable"},
	})
	got := b.String()

	contains(t, got, "\tAddPath []string // --add-path")
	contains(t, got, `command.StringListVar(fs, &f.AddPath, FlagDemoAddPath, "A path to add; repeatable")`)
	notContains(t, got, "does NOT carry", "a StringList is bound by the binder")
}

// TestWriteBinderEmitsDefaultsForARuntimeDefault covers the shape that stopped
// generated binders from hardcoding a number the live binding disagreed with.
func TestWriteBinderEmitsDefaultsForARuntimeDefault(t *testing.T) {
	var b bytes.Buffer
	writeBinder(&b, "affected plan", "affected", []command.Flag{
		{Name: "max-shards", Kind: command.Int, Default: 8, DefaultAtBind: true, Doc: "Maximum CI shards"},
		{Name: "detail", Kind: command.Bool, Doc: "Per-shard detail"},
	})
	got := b.String()

	contains(t, got, "type AffectedPlanDefaults struct {")
	contains(t, got, "\tMaxShards int // --max-shards")
	contains(t, got, "func BindAffectedPlan(fs *flag.FlagSet, d AffectedPlanDefaults) *AffectedPlanFlags {")
	// The declared Default (8) documents the man page; the binder must take the
	// caller's value instead.
	contains(t, got, `fs.IntVar(&f.MaxShards, FlagAffectedMaxShards, d.MaxShards, "Maximum CI shards")`)
	notContains(t, got, "d.Detail", "only a DefaultAtBind flag reads from Defaults")
	// The mode binds a subset of ONE command's flags, so the constants stay the
	// command's rather than the mode's.
	contains(t, got, "FlagAffectedDetail")
	notContains(t, got, "FlagAffectedPlanDetail")
}

func TestWriteBinderEmitsNothingWithoutABindableFlag(t *testing.T) {
	t.Run("no flags", func(t *testing.T) {
		var b bytes.Buffer
		writeBinder(&b, "demo", "demo", nil)
		empty(t, b.String())
	})

	// A custom-valued flag is bound by the command itself; a second fs.Var for it
	// would panic at parse time with "flag redefined".
	t.Run("only custom flags", func(t *testing.T) {
		var b bytes.Buffer
		writeBinder(&b, "demo", "demo", []command.Flag{{Name: "ignore", Kind: command.Custom}})
		empty(t, b.String())
	})
}

// A binder that silently omits a command's custom flags is how a later cleanup replaces a
// hand-binding with it and produces a command that cannot declare write paths, compiling.
func TestWriteBinderNamesTheFlagsItCannotCarry(t *testing.T) {
	var b bytes.Buffer
	writeBinder(&b, "demo", "demo", []command.Flag{
		{Name: "criteria", Kind: command.String},
		{Name: "write-paths", Kind: command.Custom},
		{Name: "deny-paths", Kind: command.Custom},
	})

	got := b.String()
	contains(t, got, "It does NOT carry --write-paths, --deny-paths")
	notContains(t, got, "f.WritePaths", "a custom flag gets no field")
}

// TestWriteBinderEmitsParsableGo is the property `emit.Go` enforces at generate time,
// asked of one command instead of the whole registry.
func TestWriteBinderEmitsParsableGo(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("package flaggen\n\nimport (\n\t\"flag\"\n\t\"time\"\n)\n\n")
	b.WriteString("const (\n\tFlagDemoTimeout = \"timeout\"\n\tFlagDemoName = \"name\"\n\tFlagDemoDepth = \"depth\"\n)\n")
	writeBinder(&b, "demo", "demo", []command.Flag{
		{Name: "timeout", Kind: command.Duration, Unit: "s", Default: 90 * time.Second, Doc: "Give up after this long"},
		{Name: "name", Kind: command.String, Default: "main", Doc: "Which one"},
		{Name: "depth", Kind: command.Int, Doc: "How deep"},
	})

	_, err := format.Source(b.Bytes())
	if err != nil {
		t.Fatalf("generated binder does not parse: %v\n%s", err, b.String())
	}
}

func TestBindersRefusesWhatTheConventionForbids(t *testing.T) {
	tree := func(f command.Flag) command.Command {
		return command.Command{Name: "tool", Children: []command.Command{{Name: "run", Flags: []command.Flag{f}}}}
	}
	cases := []struct {
		name string
		flag command.Flag
		want string
	}{
		{"a bool named no-x", command.Flag{Name: "no-cache", Kind: command.Bool, Default: false}, "declare the positive name"},
		{"a bool with no default", command.Flag{Name: "cache", Kind: command.Bool}, "bool with no Default"},
		{"a Duration with no Unit", command.Flag{Name: "timeout", Kind: command.Duration}, "no Unit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Binders(tree(c.flag), "gen")
			isError(t, err, c.name)
			contains(t, err.Error(), c.want)
		})
	}

	t.Run("a bool whose negation another flag takes", func(t *testing.T) {
		root := command.Command{Name: "tool", Children: []command.Command{{Name: "run", Flags: []command.Flag{
			{Name: "cache", Kind: command.Bool, Default: true},
			{Name: "no-cache", Kind: command.String},
		}}}}
		_, err := Binders(root, "gen")
		isError(t, err)
		contains(t, err.Error(), "negation of bool --cache")
	})
}

func TestBindersAcceptsADeclaredPositiveBool(t *testing.T) {
	root := command.Command{Name: "tool", Children: []command.Command{{Name: "run", Flags: []command.Flag{
		{Name: "cache", Kind: command.Bool, Default: true, Doc: "Replay hits"},
	}}}}
	got, err := Binders(root, "gen")
	noError(t, err)
	contains(t, string(got), `fs.BoolVar(&f.Cache, FlagRunCache, true, "Replay hits")`)
	contains(t, string(got), "command.NegatedBoolVar(fs, &f.Cache, FlagRunCache)")
	contains(t, string(got), "Cache bool // --[no-]cache")
}
