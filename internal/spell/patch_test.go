package spell

import (
	"encoding/json"
	"testing"

	"github.com/egladman/magus/types"
	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyPatch(t *testing.T) {
	// applies asserts a patch produces the expected argv.
	applies := func(t *testing.T, argv []string, ops []types.PatchOp, want []string) {
		t.Helper()
		got, err := ApplyPatch(argv, ops)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	// fails asserts a patch is rejected.
	fails := func(t *testing.T, argv []string, ops []types.PatchOp) {
		t.Helper()
		_, err := ApplyPatch(argv, ops)
		assert.Error(t, err)
	}

	t.Run("no ops", func(t *testing.T) {
		applies(t, []string{"a", "b"}, nil, []string{"a", "b"})
	})
	t.Run("append end", func(t *testing.T) {
		applies(t, []string{"run", "./..."}, []types.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}, []string{"run", "./...", "-v"})
	})
	t.Run("prepend", func(t *testing.T) {
		applies(t, []string{"build"}, []types.PatchOp{{Op: "add", Path: "/0", Value: "-x"}}, []string{"-x", "build"})
	})
	t.Run("insert middle", func(t *testing.T) {
		applies(t, []string{"run", "./..."}, []types.PatchOp{{Op: "add", Path: "/1", Value: "--fix"}}, []string{"run", "--fix", "./..."})
	})
	t.Run("replace element", func(t *testing.T) {
		applies(t, []string{"-l", "."}, []types.PatchOp{{Op: "replace", Path: "/0", Value: "-w"}}, []string{"-w", "."})
	})
	t.Run("remove element", func(t *testing.T) {
		applies(t, []string{"mod", "tidy", "--diff"}, []types.PatchOp{{Op: "remove", Path: "/2"}}, []string{"mod", "tidy"})
	})
	t.Run("two removes (rust fmt)", func(t *testing.T) {
		applies(t, []string{"fmt", "--", "--check"}, []types.PatchOp{{Op: "remove", Path: "/2"}, {Op: "remove", Path: "/1"}}, []string{"fmt"})
	})
	t.Run("compose: insert then append", func(t *testing.T) {
		applies(t, []string{"run", "./..."}, []types.PatchOp{{Op: "add", Path: "/1", Value: "--fix"}, {Op: "add", Path: "/-", Value: "-v"}}, []string{"run", "--fix", "./...", "-v"})
	})
	t.Run("move", func(t *testing.T) {
		applies(t, []string{"a", "b", "c"}, []types.PatchOp{{Op: "move", Path: "/0", From: "/2"}}, []string{"c", "a", "b"})
	})
	t.Run("copy", func(t *testing.T) {
		applies(t, []string{"a", "b"}, []types.PatchOp{{Op: "copy", Path: "/-", From: "/0"}}, []string{"a", "b", "a"})
	})
	t.Run("test pass", func(t *testing.T) {
		applies(t, []string{"go", "test"}, []types.PatchOp{{Op: "test", Path: "/0", Value: "go"}}, []string{"go", "test"})
	})
	t.Run("test fail", func(t *testing.T) {
		fails(t, []string{"go", "test"}, []types.PatchOp{{Op: "test", Path: "/0", Value: "rustc"}})
	})
	t.Run("index out of range", func(t *testing.T) {
		fails(t, []string{"a"}, []types.PatchOp{{Op: "remove", Path: "/3"}})
	})
	t.Run("replace past end", func(t *testing.T) {
		fails(t, []string{"a"}, []types.PatchOp{{Op: "replace", Path: "/1", Value: "x"}})
	})
	t.Run("add past end", func(t *testing.T) {
		fails(t, []string{"a"}, []types.PatchOp{{Op: "add", Path: "/5", Value: "x"}})
	})
	t.Run("dash on remove invalid", func(t *testing.T) {
		fails(t, []string{"a"}, []types.PatchOp{{Op: "remove", Path: "/-"}})
	})
	t.Run("leading zero invalid", func(t *testing.T) {
		fails(t, []string{"a", "b"}, []types.PatchOp{{Op: "remove", Path: "/01"}})
	})

	// Input must never be mutated.
	base := []string{"a", "b"}
	_, err := ApplyPatch(base, []types.PatchOp{{Op: "add", Path: "/-", Value: "c"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, base, "base mutated")
}

// TestApplyPatchConformance proves our flat-array applier agrees with the
// canonical RFC 6902 implementation (evanphx/json-patch) on the argv subset we
// use, so "follow the RFC" is verified, not just asserted.
func TestApplyPatchConformance(t *testing.T) {
	// agrees asserts our flat-array applier matches the reference impl.
	agrees := func(t *testing.T, argv []string, ops []types.PatchOp) {
		t.Helper()
		got, err := ApplyPatch(argv, ops)
		require.NoError(t, err)
		ref, err := applyWithEvanphx(argv, ops)
		require.NoError(t, err)
		assert.Equal(t, ref, got)
	}

	agrees(t, []string{"run", "./..."}, []types.PatchOp{{Op: "add", Path: "/-", Value: "-v"}})
	agrees(t, []string{"run", "./..."}, []types.PatchOp{{Op: "add", Path: "/1", Value: "--fix"}})
	agrees(t, []string{"-l", "."}, []types.PatchOp{{Op: "replace", Path: "/0", Value: "-w"}})
	agrees(t, []string{"mod", "tidy", "--diff"}, []types.PatchOp{{Op: "remove", Path: "/2"}})
	agrees(t, []string{"fmt", "--", "--check"}, []types.PatchOp{{Op: "remove", Path: "/2"}, {Op: "remove", Path: "/1"}})
	agrees(t, []string{"a", "b", "c"}, []types.PatchOp{{Op: "move", Path: "/0", From: "/2"}})
	agrees(t, []string{"a", "b"}, []types.PatchOp{{Op: "copy", Path: "/-", From: "/0"}})
	agrees(t, []string{"run", "./..."}, []types.PatchOp{{Op: "add", Path: "/1", Value: "--fix"}, {Op: "add", Path: "/-", Value: "-v"}})
}

// applyWithEvanphx runs ops over argv via the reference RFC 6902 library by
// marshalling argv to a JSON array document and the ops to a JSON Patch.
func applyWithEvanphx(argv []string, ops []types.PatchOp) ([]string, error) {
	doc, err := json.Marshal(argv)
	if err != nil {
		return nil, err
	}
	patchJSON, err := json.Marshal(ops)
	if err != nil {
		return nil, err
	}
	patch, err := jsonpatch.DecodePatch(patchJSON)
	if err != nil {
		return nil, err
	}
	out, err := patch.Apply(doc)
	if err != nil {
		return nil, err
	}
	var res []string
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, err
	}
	return res, nil
}

// goldenBuiltins is the original hand-maintained built-in table, frozen here as
// a golden. The built-in registry is now produced by compiling each
// spells/<name>/spell.buzz to bytecode and running it (see loadBuiltins);
// TestBuiltinsMatchGolden asserts that pipeline reproduces these exact values,
// so an accidental change in a built-in's .buzz — or in the Buzz toolchain — is
// caught. Update this table in lockstep with an intentional built-in change.
var goldenBuiltins = map[string]Descriptor{
	"bash": {
		Name:  "bash",
		Needs: []string{"**/*.sh", "**/*.bash", ".shellcheckrc"},
		Ops: map[string]types.SpellOp{
			"shellcheck": {Run: types.Run{Cmd: "sh", Args: []string{"-c", "find . \\( -name '*.sh' -o -name '*.bash' \\) -print0 | xargs -0 -r shellcheck"}}},
		},
	},
	"buf": {
		Name:       "buf",
		Needs:      []string{"**/*.proto", "buf.yaml", "buf.gen.yaml", "buf.work.yaml", "buf.lock"},
		Provides:   []string{"gen/**"},
		VersionCmd: []string{"buf", "--version"},
		Ops: map[string]types.SpellOp{
			"buf-build":    {Run: types.Run{Cmd: "buf", Args: []string{"build"}}},
			"buf-generate": {Run: types.Run{Cmd: "buf", Args: []string{"generate"}}},
			"buf-lint": {Run: types.Run{Cmd: "buf", Args: []string{"lint"}, Charms: map[string]types.Charm{
				"gha": {Ops: []types.PatchOp{{Op: "add", Path: "/-", Value: "--error-format=github-actions"}}},
			}}},
			"buf-format": {Run: types.Run{Cmd: "buf", Args: []string{"format", "--exit-code"}, Charms: map[string]types.Charm{
				"rw": {Ops: []types.PatchOp{{Op: "replace", Path: "/1", Value: "-w"}}},
			}}},
		},
	},
	"buzz": {
		Name:  "buzz",
		Needs: []string{"**/*.buzz"},
		Ops: map[string]types.SpellOp{
			"buzz-check": {Run: types.Run{Cmd: "sh", Args: []string{"-c", "find . -name '*.buzz' -print0 | xargs -0 -r -n1 buzz --check"}}},
			"buzz-test":  {Run: types.Run{Cmd: "sh", Args: []string{"-c", "find . -name '*.buzz' -print0 | xargs -0 -r -n1 buzz --test"}}},
			"magus-buzz": {Run: types.Run{Cmd: "sh", Args: []string{"-c", "find . -name '*.buzz' -print0 | xargs -0 -r -n1 \"$MAGUS\" buzz"}}},
		},
	},
	"cosign": {
		Name:       "cosign",
		VersionCmd: []string{"cosign", "version"},
		Ops: map[string]types.SpellOp{
			"cosign-sign":   {Run: types.Run{Cmd: "cosign", Args: []string{"sign", "--yes"}}},
			"cosign-verify": {Run: types.Run{Cmd: "cosign", Args: []string{"verify"}}},
			"cosign-attest": {Run: types.Run{Cmd: "cosign", Args: []string{"attest", "--yes"}}},
		},
	},
	"docker": {
		Name:       "docker",
		Needs:      []string{"Dockerfile", ".dockerignore", "**/*"},
		VersionCmd: []string{"docker", "--version"},
		Ops: map[string]types.SpellOp{
			"docker-build":       {Run: types.Run{Cmd: "docker", Args: []string{"build"}}},
			"docker-buildx":      {Run: types.Run{Cmd: "docker", Args: []string{"buildx", "build"}}},
			"docker-build-check": {Run: types.Run{Cmd: "docker", Args: []string{"build", "--check"}}},
			"hadolint":           {Run: types.Run{Cmd: "hadolint", Args: []string{"Dockerfile"}}},
		},
	},
	"golang": {
		Name:       "go",
		Needs:      []string{"**/*.go", "go.mod", "go.sum", "go.work", "go.work.sum"},
		VersionCmd: []string{"go", "version"},
		Ops: map[string]types.SpellOp{
			"go-build":    {Run: types.Run{Cmd: "go", Args: []string{"build"}}},
			"go-clean":    {Run: types.Run{Cmd: "go", Args: []string{"clean", "./..."}}},
			"go-generate": {Run: types.Run{Cmd: "go", Args: []string{"generate", "./..."}}},
			"go-fmt": {Run: types.Run{Cmd: "gofmt", Args: []string{"-l", "."}, Charms: map[string]types.Charm{
				"rw": {Ops: []types.PatchOp{{Op: "replace", Path: "/0", Value: "-w"}}},
			}}},
			"golangci-lint": {Run: types.Run{Cmd: "go", Args: []string{"tool", "golangci-lint", "run", "./..."}, Charms: map[string]types.Charm{
				"debug": {Ops: []types.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
				"rw":    {Ops: []types.PatchOp{{Op: "add", Path: "/3", Value: "--fix"}}},
			}}},
			"go-test": {Run: types.Run{Cmd: "go", Args: []string{"test", "./..."}, Charms: map[string]types.Charm{
				"debug": {Ops: []types.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
				"cd": {Ops: []types.PatchOp{
					{Op: "add", Path: "/-", Value: "-covermode=atomic"},
					{Op: "add", Path: "/-", Value: "-coverprofile=coverage.out"},
				}},
			}}},
			// tidy checks by default (--diff exits non-zero if go.mod/go.sum need
			// changes — safe for CI gating); the write charm applies the changes.
			"go-mod-tidy": {Run: types.Run{Cmd: "go", Args: []string{"mod", "tidy", "--diff"}, Charms: map[string]types.Charm{
				"rw": {Ops: []types.PatchOp{{Op: "remove", Path: "/2"}}},
			}}},
			"go-vet":      {Run: types.Run{Cmd: "go", Args: []string{"vet", "./..."}}},
			"govulncheck": {Run: types.Run{Cmd: "go", Args: []string{"tool", "govulncheck", "./..."}}},
		},
	},
	"markdown": {
		Name:   "md",
		Needs:  []string{"**/*.md", "**/*.MD", "**/*.markdown", ".markdownlint.json", ".markdownlint.yaml"},
		Claims: []string{"**/*.md", "**/*.mdx"},
		Ops: map[string]types.SpellOp{
			"markdownlint": {Run: types.Run{Cmd: "markdownlint", Args: []string{"**/*.md", "**/*.mdx"}}},
			"prettier": {Run: types.Run{Cmd: "prettier", Args: []string{"--check", "--no-error-on-unmatched-pattern", "**/*.md", "**/*.mdx"}, Charms: map[string]types.Charm{
				"rw": {Ops: []types.PatchOp{{Op: "replace", Path: "/0", Value: "--write"}}},
			}}},
		},
	},
	"python": {
		Name:       "py",
		Needs:      []string{"**/*.py", "pyproject.toml", "requirements.txt", "requirements-*.txt", "Pipfile", "Pipfile.lock", "setup.py", "setup.cfg", "uv.lock", "poetry.lock"},
		VersionCmd: []string{"python3", "--version"},
		Ops: map[string]types.SpellOp{
			"uv-build": {Run: types.Run{Cmd: "uv", Args: []string{"build"}}},
			"uv-clean": {Run: types.Run{Cmd: "uv", Args: []string{"clean"}}},
			"pytest": {Run: types.Run{Cmd: "uv", Args: []string{"run", "pytest"}, Charms: map[string]types.Charm{
				"debug": {Ops: []types.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
			}}},
			"ruff-check": {Run: types.Run{Cmd: "uv", Args: []string{"run", "ruff", "check", "."}, Charms: map[string]types.Charm{
				"debug": {Ops: []types.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
				"rw":    {Ops: []types.PatchOp{{Op: "add", Path: "/3", Value: "--fix"}}},
				"gha":   {Ops: []types.PatchOp{{Op: "add", Path: "/3", Value: "--output-format=github"}}},
			}}},
			"ruff-format": {Run: types.Run{Cmd: "uv", Args: []string{"run", "ruff", "format", "--check", "."}, Charms: map[string]types.Charm{
				"rw": {Ops: []types.PatchOp{{Op: "remove", Path: "/3"}}},
			}}},
		},
	},
	"rust": {
		Name:       "rs",
		Needs:      []string{"**/*.rs", "Cargo.toml", "Cargo.lock"},
		VersionCmd: []string{"rustc", "--version"},
		Ops: map[string]types.SpellOp{
			"cargo-build":  {Run: types.Run{Cmd: "cargo", Args: []string{"build", "--release"}}},
			"cargo-clean":  {Run: types.Run{Cmd: "cargo", Args: []string{"clean"}}},
			"cargo-clippy": {Run: types.Run{Cmd: "cargo", Args: []string{"clippy", "--", "-D", "warnings"}}},
			"cargo-fmt": {Run: types.Run{Cmd: "cargo", Args: []string{"fmt", "--", "--check"}, Charms: map[string]types.Charm{
				"rw": {Ops: []types.PatchOp{{Op: "remove", Path: "/2"}, {Op: "remove", Path: "/1"}}},
			}}},
			"cargo-test": {Run: types.Run{Cmd: "cargo", Args: []string{"test"}}},
		},
	},
	"typescript": {
		Name:       "ts",
		Needs:      []string{"**/*.ts", "**/*.tsx", "**/*.js", "**/*.jsx", "**/*.json", "package.json", ".npmrc", "pnpm-lock.yaml", "package-lock.json", "npm-shrinkwrap.json"},
		Claims:     []string{"**/*.ts", "**/*.tsx", "**/*.mts", "**/*.cts", "**/*.js", "**/*.mjs", "**/*.cjs", "**/*.jsx", "**/*.json", "**/*.jsonc", "**/*.md", "**/*.mdx", "**/*.yaml", "**/*.yml", "**/*.css", "**/*.scss", "**/*.html"},
		Opaque:     true,
		VersionCmd: []string{"node", "--version"},
		Ops: map[string]types.SpellOp{
			"eslint":    {Run: types.Run{Cmd: "pnpm", Args: []string{"exec", "eslint", "."}}},
			"preflight": {},
			"prettier": {Run: types.Run{Cmd: "pnpm", Args: []string{"exec", "prettier", "--check", "."}, Charms: map[string]types.Charm{
				"rw": {Ops: []types.PatchOp{{Op: "replace", Path: "/2", Value: "--write"}}},
			}}},
			"tsc": {Run: types.Run{Cmd: "pnpm", Args: []string{"exec", "tsc"}}},
			"vitest": {Run: types.Run{Cmd: "pnpm", Args: []string{"exec", "vitest", "run"}, Charms: map[string]types.Charm{
				"gha": {Ops: []types.PatchOp{{Op: "add", Path: "/-", Value: "--reporter=github-actions"}}},
			}}},
		},
	},
}

func TestBuiltinsMatchGolden(t *testing.T) {
	got := builtinsByDir()
	require.Len(t, got, len(goldenBuiltins), "registry/golden size mismatch")
	for dir, want := range goldenBuiltins {
		g, ok := got[dir]
		if !assert.Truef(t, ok, "registry missing built-in %q", dir) {
			continue
		}
		// DocTargets is resolution-path metadata (which targets are function
		// handlers), not part of a spell's semantic identity, and is not pinned by
		// this golden; clear it before comparing the semantic fields.
		g.DocOps = nil
		assert.Equalf(t, want, g, "built-in %q", dir)
	}
}
