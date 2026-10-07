package spell

import (
	"context"
	"errors"
	"github.com/egladman/magus/internal/interp/bindings/ffi"
	json "github.com/egladman/magus/internal/json"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func TestBuiltins_NonEmpty(t *testing.T) {
	m := Builtins()
	require.NotEmpty(t, m, "Builtins() returned empty map")
	for key, s := range m {
		assert.NotEmptyf(t, s.Name, "Builtins()[%q].Name is empty", key)
		// The registry is keyed by runtime name, so the key is the spell's Name.
		assert.Equalf(t, key, s.Name, "Builtins() key %q != spells.Descriptor.Name %q", key, s.Name)
	}
}

func TestBuiltins_KeyedByName(t *testing.T) {
	m := Builtins()
	// The golang spell renames itself to "go": it must be reachable by name…
	assert.Contains(t, m, "go", `Builtins()["go"] not found`)
	// …and not by its source directory.
	assert.NotContains(t, m, "golang", `Builtins()["golang"] present — registry is keyed by name, not source dir`)
}

// Every top-level shipped spell loads in the built-in session or is refused there for a
// host import; nothing else. A top-level directory with no spell.buzz holds nested
// spells, which never register. The sets are pinned because a built-in that grows a
// host import leaves the registry without an error, and because embedding the nested
// providers must not change the registry, BuiltinsHash, or the cache keys it feeds.
func TestShippedSpellsLoad(t *testing.T) {
	entries, err := fs.ReadDir(spells.Shipped(), ".")
	require.NoError(t, err)
	var sourceOnly, nesting []string
	for _, e := range entries {
		src, err := fs.ReadFile(spells.Shipped(), e.Name()+"/spell.buzz")
		if errors.Is(err, fs.ErrNotExist) {
			nesting = append(nesting, e.Name())
			continue
		}
		require.NoError(t, err)
		spec, err := compileBuiltin(t.Context(), string(src))
		if errors.As(err, new(hostImportError)) {
			sourceOnly = append(sourceOnly, e.Name())
			continue
		}
		require.NoError(t, err, "spells/%s", e.Name())
		assert.Equal(t, Builtins()[spec.Name], spec, "spells/%s", e.Name())
	}
	assert.ElementsMatch(t, []string{
		"bash", "buf", "buzz", "cosign", "docker", "go", "markdown", "podman", "python", "rust", "typescript",
	}, slices.Collect(maps.Keys(Builtins())))
	assert.Equal(t, []string{"endoflife-date", "onepassword", "system-keychain"}, sourceOnly)
	assert.Equal(t, []string{"aws", "github", "gitlab", "harness"}, nesting)
}

// The built-in session offers the spell type modules and nothing else, so its own
// import failure (BZZ2001) is what keeps a host-importing spell out of the registry.
// Any other failure is a broken spell, which must not read as source-only.
func TestBuiltinSessionRefusesHostImports(t *testing.T) {
	const name = "export fun mgs_getName() > str { return \"x\"; }\n"
	for module, src := range map[string]string{
		"proc":        "import \"magus/spell\";\nimport \"proc\";\n" + name,
		"http":        "import \"http\";\n" + name,
		"spells/peer": "import \"spells/peer\";\n" + name,
	} {
		_, err := compileBuiltin(t.Context(), src)
		require.ErrorIs(t, err, buzz.UnresolvedImport, module)
		var hostErr hostImportError
		require.ErrorAs(t, err, &hostErr)
		assert.Equal(t, module, hostErr.module)
	}

	spec, err := compileBuiltin(t.Context(), "import \"magus/spell\";\nimport \"magus/charm\";\nimport \"magus/lint\";\n"+name)
	require.NoError(t, err)
	assert.Equal(t, "x", spec.Name)

	_, err = compileBuiltin(t.Context(), "export fun mgs_getName() > str { return 1; }\n")
	require.Error(t, err)
	assert.False(t, errors.As(err, new(hostImportError)), "a type error is a broken spell: %v", err)
}

// BenchmarkLoadShipped is the startup cost every workspace-loading process pays for
// the built-in registry.
func BenchmarkLoadShipped(b *testing.B) {
	for b.Loop() {
		loadShipped()
	}
}

func TestShippedDir(t *testing.T) {
	for name, want := range map[string]string{
		"go":             "golang",
		"bash":           "bash",
		"endoflife-date": "endoflife-date", // ships as source only, found by its directory
		"harness/cursor": "harness/cursor", // nested, so source only
		"harness":        "",               // holds spells, is not one
		"golang":         "",               // the go built-in's directory, not a spell name
		"nope":           "",
	} {
		dir, ok := ShippedDir(name)
		assert.Equal(t, want, dir, name)
		assert.Equal(t, want != "", ok, name)
	}
}

// TestBuiltinCommentSyntax pins the decoded syntax.comments declarations
// internal/ci's gate tests mirror as literals; a drift between a spell's
// declaration and those literals fails here, where the declaration decodes.
func TestBuiltinCommentSyntax(t *testing.T) {
	m := Builtins()
	require.NotNil(t, m["go"].Syntax, "the go spell declares its syntax")
	goSyn := m["go"].Syntax.Comments
	require.NotNil(t, goSyn, "the go spell declares its comment syntax")
	assert.Equal(t, []string{".go"}, m["go"].LanguageExtensions, "only .go: the spell's other claims (.s, .c, .h, .txtar) are not Go")
	assert.Equal(t, &spells.CommentSyntax{
		LineComments:  []string{"//"},
		BlockComments: []spells.CommentBlock{{Open: "/*", Close: "*/"}},
		Quotes: []spells.Quote{
			{Open: "`", Close: "`", IgnoreEscape: true},
			{Open: `"`, Close: `"`},
			{Open: "'", Close: "'"},
		},
		Directives: []string{"go:", "nolint", "export", "line ", "+build", "sys", "extern"},
	}, goSyn)

	require.NotNil(t, m["buzz"].Syntax, "the buzz spell declares its syntax")
	require.NotNil(t, m["buzz"].Syntax.Comments, "the buzz spell declares its comment syntax")
	assert.Equal(t, []string{".buzz"}, m["buzz"].LanguageExtensions)

	require.NotNil(t, m["python"].Syntax, "the python spell declares its syntax")
	pySyn := m["python"].Syntax.Comments
	require.NotNil(t, pySyn, "the python spell declares its comment syntax")
	assert.Equal(t, []string{"type:", "noqa"}, pySyn.Directives)
	assert.Nil(t, m["bash"].Syntax, "bash declares none on purpose: heredocs defeat the declaration shape")
}

func TestBuiltinsHash_Format(t *testing.T) {
	h := BuiltinsHash()
	assert.Len(t, h, 64, "BuiltinsHash() should be 64 chars (SHA-256 hex)")
	for _, c := range h {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			assert.Failf(t, "non-hex character", "BuiltinsHash() contains non-hex character %q", c)
			break
		}
	}
}

func TestBuiltinsHash_Stable(t *testing.T) {
	h1, h2 := BuiltinsHash(), BuiltinsHash()
	assert.Equal(t, h1, h2, "BuiltinsHash() not stable")
}

func TestGoSpell_TidyTarget(t *testing.T) {
	goSpell := Builtins()["go"]
	tidy, ok := goSpell.Ops["go-mod-tidy"]
	require.Truef(t, ok, "go spell has no go-mod-tidy target; targets: %v", goSpell.OpNames())
	// Default (no write charm): check mode via --diff (non-zero exit if changes
	// are needed — safe for CI gating).
	// update, not rw: tidy re-resolves against the proxy, so its result depends on what
	// upstream serves rather than on this tree. The charm drops --diff (remove /2) so
	// tidy actually applies the changes.
	assert.Equal(t, spells.Op{
		Command: spells.Command{
			Bin:    "go",
			Args:   []string{"mod", "tidy", "--diff"},
			Charms: map[string]spells.Charm{"update": {Ops: []spells.PatchOp{{Op: "remove", Path: "/2"}}}},
		},
		Doc: tidy.Doc,
	}, tidy)
	_, hasRW := tidy.Charms["rw"]
	assert.False(t, hasRW, "tidy must not carry rw: default_charms: [rw] would re-resolve dependencies on unrelated runs")
}

// TestBuiltinCharmsUnchanged pins every bundled spell's charm patches after the
// migration from hand-written positional JSON Pointers to magus/charm
// constructors: the resolved RFC 6902 ops must be byte-identical to the originals,
// so the rewrite is provably behavior-preserving (e.g. after(args,"run",["--fix"])
// still lands --fix at /3; set(args,"--check","--write") still replaces /2).
func TestBuiltinCharmsUnchanged(t *testing.T) {
	specs := Builtins()

	charm := func(t *testing.T, spell, op, ch string) []spells.PatchOp {
		t.Helper()
		sp, ok := specs[spell]
		require.Truef(t, ok, "spell %q missing", spell)
		o, ok := sp.Ops[op]
		require.Truef(t, ok, "%s op %q missing", spell, op)
		c, ok := o.Charms[ch]
		require.Truef(t, ok, "%s op %q charm %q missing", spell, op, ch)
		return c.Ops
	}

	cases := []struct {
		spell, op, charm string
		want             []spells.PatchOp
	}{
		// go
		{"go", "go-fmt", "rw", []spells.PatchOp{{Op: "replace", Path: "/0", Value: "-w"}}},
		{"go", "golangci-lint", "debug", []spells.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
		// /1, not /3: golangci-lint runs from PATH now, so the argv lost the leading
		// "tool", "golangci-lint" prefix and --fix inserts right after "run".
		{"go", "golangci-lint", "rw", []spells.PatchOp{{Op: "add", Path: "/1", Value: "--fix"}}},
		{"go", "go-test", "cd", []spells.PatchOp{
			{Op: "add", Path: "/-", Value: "-covermode=atomic"},
			{Op: "add", Path: "/-", Value: "-coverprofile=coverage.out"},
		}},
		{"go", "go-mod-tidy", "update", []spells.PatchOp{{Op: "remove", Path: "/2"}}},
		// py
		{"python", "pytest", "debug", []spells.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
		{"python", "ruff-check", "rw", []spells.PatchOp{{Op: "add", Path: "/3", Value: "--fix"}}},
		{"python", "ruff-check", "gha", []spells.PatchOp{{Op: "add", Path: "/3", Value: "--output-format=github"}}},
		{"python", "ruff-format", "rw", []spells.PatchOp{{Op: "remove", Path: "/3"}}},
		// ts
		{"typescript", "prettier", "rw", []spells.PatchOp{{Op: "replace", Path: "/2", Value: "--write"}}},
		{"typescript", "vitest", "gha", []spells.PatchOp{{Op: "add", Path: "/-", Value: "--reporter=github-actions"}}},
		{"typescript", "eslint", "rw", []spells.PatchOp{{Op: "add", Path: "/2", Value: "--fix"}}},
		{"typescript", "eslint", "gha", []spells.PatchOp{{Op: "add", Path: "/2", Value: "--format=unix"}}},
		{"typescript", "biome-check", "rw", []spells.PatchOp{{Op: "add", Path: "/3", Value: "--write"}}},
		{"typescript", "biome-check", "gha", []spells.PatchOp{{Op: "add", Path: "/3", Value: "--reporter=github"}}},
		{"typescript", "biome-format", "rw", []spells.PatchOp{{Op: "add", Path: "/3", Value: "--write"}}},
		// md
		{"markdown", "prettier", "rw", []spells.PatchOp{{Op: "replace", Path: "/0", Value: "--write"}}},
		// buf
		{"buf", "buf-lint", "gha", []spells.PatchOp{{Op: "add", Path: "/-", Value: "--error-format=github-actions"}}},
		{"buf", "buf-format", "rw", []spells.PatchOp{{Op: "replace", Path: "/1", Value: "-w"}}},
		// rs — compound charm (two drops): the constructor concat must still yield
		// remove /2 then remove /1, in that order.
		{"rust", "cargo-fmt", "rw", []spells.PatchOp{{Op: "remove", Path: "/2"}, {Op: "remove", Path: "/1"}}},
	}

	for _, c := range cases {
		t.Run(c.spell+"/"+c.op+"/"+c.charm, func(t *testing.T) {
			assert.Equal(t, c.want, charm(t, c.spell, c.op, c.charm))
		})
	}
}

// TestTSRequiredGlobsCoverModuleVariants guards D1/D2: mgs_listRequiredGlobs must
// cover every module-variant extension and lockfile format, so editing a
// .mts/.cts/.mjs/.cjs file or bumping a yarn/bun lockfile invalidates the cache
// instead of silently missing it.
func TestTSRequiredGlobsCoverModuleVariants(t *testing.T) {
	ts, ok := Builtins()["typescript"]
	require.True(t, ok, "ts spell missing")

	for _, want := range []string{
		"**/*.mts", "**/*.cts", "**/*.mjs", "**/*.cjs", "yarn.lock", "bun.lockb", "tsconfig*.json",
	} {
		assert.Containsf(t, ts.Needs, want, "ts required globs missing %q", want)
	}
}

// TestBuiltinTargetNamesAreCanonical guards a hole the decode-time normalization
// does not cover. Op keys authored in Buzz are canonicalized by Decode, but
// spells.WithTargets takes whatever a Go caller hands it, and it cannot call
// types.Normalize, because types imports spells and the reverse would cycle.
//
// A non-canonical name here is not a style problem: every request reaching
// dispatchOp has been kebab-normalized by ParseTarget, and dispatch is a map hit,
// so a target registered as "goBuild" is looked up as "go-build", missed, and
// swallowed as a fan-out skip at debug level. Declared, and reachable by nothing.
// Failing here is how that stays impossible rather than merely unlikely.
func TestBuiltinTargetNamesAreCanonical(t *testing.T) {
	for name, d := range Builtins() {
		assert.Equalf(t, types.Normalize(name), name, "spell %q is not canonically named", name)
		for _, op := range d.OpNames() {
			assert.Equalf(t, types.Normalize(op), op,
				"spell %q op %q is not canonical - it would be unreachable by any request", name, op)
		}
	}
}

// TestCharmBuzzParityWithHost keeps the pure-Buzz magus/charm module
// (internal/spell/charm.buzz) in lockstep with the Go charm host module
// (std/charm.go): every constructor magus/charm exports must produce a
// byte-identical RFC 6902 patch record. The Buzz module is hand-written (charm is
// logic, not a struct, so it can't be codegen'd), so this guard is what licenses
// the duplication — diverge the two and this fails.
func TestCharmBuzzParityWithHost(t *testing.T) {
	ctx := context.Background()

	// eval loads the magus/charm source and evaluates a bare constructor call
	// (exports are flat-imported, like magus/spell's Target), returning the
	// marshalled Go value.
	eval := func(t *testing.T, expr string) any {
		t.Helper()
		s := buzz.NewSession(ctx, buzz.WithEmbedded())
		defer s.Close()
		// charm.buzz imports magus/spell for the Charm/PatchOp object types; register
		// the same bundle the runtime does so the import resolves in this bare session.
		s.SetModuleDecls(SpellModulePath, SpellModuleSource)
		require.NoError(t, s.Exec(ctx, CharmModuleSource), "load charm.buzz")
		require.NoError(t, s.Exec(ctx, "final __r = "+expr+";"), "eval %s", expr)
		return ffi.ValueToAny(s.GetGlobal("__r"))
	}
	// spells.Charm now, not map[string]any: the host constructors return the typed value
	// the Buzz side already mirrored. norm marshals both sides through spells.Charm
	// anyway, so the comparison is unchanged.
	ok := func(v spells.Charm, err error) any {
		t.Helper()
		require.NoError(t, err)
		return v
	}
	// norm collapses both shapes (the host's spells.Charm and the Buzz Charm object's
	// field map) through spells.Charm, so the comparison ignores whether an empty
	// value/fromPtr key is present (the object carries all fields; the host omits
	// empties) and pins only the RFC 6902 content.
	norm := func(v any) spells.Charm {
		t.Helper()
		b, err := json.Marshal(v)
		require.NoError(t, err)
		var c spells.Charm
		require.NoError(t, json.Unmarshal(b, &c))
		return c
	}

	argv := []string{"tool", "golangci-lint", "run", "./..."}

	cases := []struct {
		name string
		expr string
		want any
	}{
		{"append", `append(["-v","-x"])`, ok(std.CharmAppend(ctx, []string{"-v", "-x"}))},
		{"prepend", `prepend(["a","b"])`, ok(std.CharmPrepend(ctx, []string{"a", "b"}))},
		{"after", `after(["tool","golangci-lint","run","./..."], "run", ["--fix"])`, ok(std.CharmAfter(ctx, argv, "run", []string{"--fix"}))},
		{"before", `before(["tool","golangci-lint","run","./..."], "run", ["--fix"])`, ok(std.CharmBefore(ctx, argv, "run", []string{"--fix"}))},
		{"set", `set(["-l","."], "-l", "-w")`, ok(std.CharmSet(ctx, []string{"-l", "."}, "-l", "-w"))},
		{"drop", `drop(["mod","tidy","--diff"], "--diff")`, ok(std.CharmDrop(ctx, []string{"mod", "tidy", "--diff"}, "--diff"))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, norm(c.want), norm(eval(t, c.expr)))
		})
	}
}

func TestValidatePatch(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		require.NoError(t, spells.ValidatePatch(nil))
	})
	t.Run("add end", func(t *testing.T) {
		require.NoError(t, spells.ValidatePatch([]spells.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}))
	})
	t.Run("replace index", func(t *testing.T) {
		require.NoError(t, spells.ValidatePatch([]spells.PatchOp{{Op: "replace", Path: "/0", Value: "-w"}}))
	})
	t.Run("remove index", func(t *testing.T) {
		require.NoError(t, spells.ValidatePatch([]spells.PatchOp{{Op: "remove", Path: "/2"}}))
	})
	t.Run("move", func(t *testing.T) {
		require.NoError(t, spells.ValidatePatch([]spells.PatchOp{{Op: "move", Path: "/0", From: "/1"}}))
	})
	t.Run("copy", func(t *testing.T) {
		require.NoError(t, spells.ValidatePatch([]spells.PatchOp{{Op: "copy", Path: "/0", From: "/1"}}))
	})
	t.Run("test", func(t *testing.T) {
		require.NoError(t, spells.ValidatePatch([]spells.PatchOp{{Op: "test", Path: "/0", Value: "go"}}))
	})
	t.Run("unknown op", func(t *testing.T) {
		assert.Error(t, spells.ValidatePatch([]spells.PatchOp{{Op: "patch", Path: "/0"}}))
	})
	t.Run("root path rejected", func(t *testing.T) {
		assert.Error(t, spells.ValidatePatch([]spells.PatchOp{{Op: "replace", Path: "", Value: "x"}}))
	})
	t.Run("path without slash", func(t *testing.T) {
		assert.Error(t, spells.ValidatePatch([]spells.PatchOp{{Op: "add", Path: "0", Value: "x"}}))
	})
	t.Run("move without from", func(t *testing.T) {
		assert.Error(t, spells.ValidatePatch([]spells.PatchOp{{Op: "move", Path: "/0"}}))
	})
	t.Run("copy bad from", func(t *testing.T) {
		assert.Error(t, spells.ValidatePatch([]spells.PatchOp{{Op: "copy", Path: "/0", From: "1"}}))
	})
}

func TestDescriptor_TargetNames(t *testing.T) {
	m := spells.Descriptor{
		Name: "test",
		Ops: map[string]spells.Op{
			"vet":   {},
			"build": {},
			"test":  {},
		},
	}
	assert.Equal(t, []string{"build", "test", "vet"}, m.OpNames())
}

func TestDescriptor_TargetNamesEmpty(t *testing.T) {
	m := spells.Descriptor{Name: "empty"}
	assert.Empty(t, m.OpNames(), "OpNames() on empty Ops should be empty")
}

// The docker spell drives two binaries and only one of them talks to a daemon. That
// asymmetry is the whole reason readiness is keyed by TOOL rather than by spell:
// a spell-scoped probe would make a Dockerfile lint wait on a service it never uses.
func TestDockerReadinessIsScopedToTheDaemonBackedTool(t *testing.T) {
	d, ok := Builtins()["docker"]
	require.True(t, ok, "docker spell not registered")

	tool, ok := d.Tools["docker"]
	probe := tool.Ready
	require.True(t, ok, "docker spell declares no docker tool")
	require.NotEmpty(t, probe.Bin, "docker declares no readiness probe")
	assert.Equal(t, spells.Command{Bin: "docker", Args: []string{"info"}}, probe,
		"`docker --version` is client-only and cannot detect a stopped daemon")

	gated := d.Tools["hadolint"].Ready.Bin != ""
	assert.False(t, gated, "linting a Dockerfile must not wait on the docker daemon")
}

// Every op resolves its probe through the bin it already declares, so no op restates
// which tool it uses.
func TestReadinessResolvesThroughOpBin(t *testing.T) {
	d := Builtins()["docker"]
	for name, op := range d.Ops {
		gated := d.Tools[op.Command.Bin].Ready.Bin != ""
		if op.Bin == "docker" {
			assert.True(t, gated, "op %q runs docker and should be gated", name)
		}
	}
}

// A spell that declares nothing behaves exactly as before.
func TestSpellsWithoutReadinessAreUngated(t *testing.T) {
	for _, name := range []string{"go", "rust", "typescript"} {
		s, ok := Builtins()[name]
		require.True(t, ok, name)
		for tool, tl := range s.Tools {
			assert.Empty(t, tl.Ready.Bin,
				"%s: %s is self-contained and needs no readiness probe", name, tool)
		}
	}
}

// The end-to-end check the enum was adopted for: a built-in spell writes
// `VersionKey{upTo = VersionComponent.patch}` in Buzz, and it must arrive in Go as
// VersionPatch. An enum case is a heap object rather than a string, so before the
// adapter unwrapped it this decoded as absent, silently, which is exactly the failure
// a bare string invited and the enum was meant to end.
func TestBuiltinSpellsDecodeVersionKeyFromEnum(t *testing.T) {
	reg := Builtins()

	goSpell, ok := reg["go"]
	require.True(t, ok, "go spell not registered")
	assert.Equal(t, spells.VersionPatch, goSpell.Tools["go"].Key.UpTo,
		"`go version` prints the host platform; patch extraction is what sheds it")
	assert.Equal(t, spells.VersionPatch, goSpell.Tools["golangci-lint"].Key.UpTo,
		"golangci-lint pads its version line with a commit and a build timestamp")

	// govulncheck declares nothing on purpose: its verdict comes from the vulnerability
	// database, whose date is in the probe output and would not survive extraction.
	assert.True(t, goSpell.Tools["govulncheck"].Key.IsZero(),
		"govulncheck must key on its whole output")

	for _, name := range []string{"docker", "rust"} {
		s, ok := reg[name]
		require.True(t, ok, "%s spell not registered", name)
		assert.Equal(t, spells.VersionPatch, s.Tools[map[string]string{"docker": "docker", "rust": "rustc"}[name]].Key.UpTo, "%s", name)
	}

	// A spell that declares nothing keeps the whole-output default.
	if bash, ok := reg["bash"]; ok {
		assert.Empty(t, bash.Tools)
	}
}

// TestBuiltinToolsNeverKeyAnObservationAsAVersion is MGS1037's invariant, checked over
// the SHIPPED registry rather than over one workspace's bindings.
//
// The doctor check walks projects -> ResolvedSpells, so it only ever sees a spell some
// magusfile imports. rust, python, aws/s3-cache and onepassword are compiled into this
// binary and bound by nothing here, so the same defect reintroduced in any of them would
// ship green. This is the wider half; MGS1037 is the half that can see a USER's spell.
func TestBuiltinToolsNeverKeyAnObservationAsAVersion(t *testing.T) {
	for name, d := range Builtins() {
		for tool, tl := range d.Tools {
			if tl.Probe.Bin == "" || tl.Observe.Bin == "" {
				continue
			}
			if tl.Probe.Bin != tl.Observe.Bin || !slices.Equal(tl.Probe.Args, tl.Observe.Args) {
				continue
			}
			assert.Falsef(t, tl.Key.IsZero(),
				"%s: %s declares `%s %s` as both probe and observe with no key, so the feed's clock keys every "+
					"target in every project binding the spell (MGS1037)",
				name, tool, tl.Probe.Bin, strings.Join(tl.Probe.Args, " "))
		}
	}
}

func TestGoFuzzOp(t *testing.T) {
	op, ok := Builtins()["go"].Ops["go-fuzz"]
	require.True(t, ok, "the go spell declares go-fuzz")

	assert.Equal(t, spells.Command{
		Bin:     "go",
		Args:    []string{"test", "-trimpath", "-run", "^$"},
		EnvKeys: []string{"GOOS", "GOARCH", "GOARM", "GOAMD64"},
		// A randomized run is never replayable; go-fuzz asks its target for skip_cache.
		External: spells.ExternalReads,
		Hints:    op.Hints,
	}, op.Command)
	assert.NotEmpty(t, op.Hints, "go-fuzz shares go-test's module failure advice")

	// The package, target name and fuzztime ride in as call-site args, so a bare run
	// carries no default package pattern that go would refuse beside -fuzz.
	assert.Empty(t, op.DefaultArgs)
	assert.Empty(t, op.TrailingArgs)
}

func TestGoFuzzMatchesGoTestPolicy(t *testing.T) {
	ops := Builtins()["go"].Ops
	fuzz, test := ops["go-fuzz"], ops["go-test"]

	// The policy fields: the same platform env keys the cache key already carries, and
	// the same failure advice, under the same binary.
	policy := func(op spells.Op) spells.Command {
		return spells.Command{Bin: op.Bin, EnvKeys: op.EnvKeys, Hints: op.Hints}
	}
	assert.Equal(t, policy(test), policy(fuzz))
}

// The records run on their own; a failure here means the generated declarations name a
// type they do not declare, or are stale.
func TestMagusDeclarationsRun(t *testing.T) {
	records, err := magusRecords()
	require.NoError(t, err, "regenerate with `magus run spells-generate`")
	var names []string
	for _, r := range records {
		names = append(names, r.name)
	}
	assert.Subset(t, names, []string{"DirsOptions", "NeighborhoodOptions", "PathOptions", "CheckStatus", "TargetRun", "Run"})
	assert.NotContains(t, names, "Context", "the target context is the host's to build")
}

// Every magus declaration names only types the declarations define, the hand-written
// Context and Exec included, which run nowhere and so reach no other check.
func TestMagusDeclSourceNamesDeclaredTypes(t *testing.T) {
	sess := buzz.NewSession(t.Context(), buzz.WithEmbedded())
	t.Cleanup(func() { _ = sess.Close() })
	sess.SetNativeModule("magus", vm.NewMap())
	DeclareMagusTypes(sess, nil)
	require.NoError(t, sess.Exec(t.Context(), `import "magus";
fun f(ctx: magus\Context, run: magus\Run) > magus\Exec { return ctx.withEnv({"k": run.trigger}); }`))
}

func TestDeclareMagusTypesDropsUnimplementedExterns(t *testing.T) {
	sess := buzz.NewSession(t.Context(), buzz.WithEmbedded())
	t.Cleanup(func() { _ = sess.Close() })
	sess.SetNativeModule("magus", vm.NewMap())
	DeclareMagusTypes(sess, func(member string) bool { return member != "affected" })
	require.NoError(t, sess.Exec(t.Context(), `import "magus";
fun kept(opts: magus\DirsOptions) > str { return opts.language; }`))
	err := sess.Exec(t.Context(), `fun dropped() > any { return magus\affected(); }`)
	assert.ErrorContains(t, err, `module magus has no member "affected"`)
}
