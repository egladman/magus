package spellruntime

import (
	"testing"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/spells"
	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExplainCharms(t *testing.T) {
	base := []string{"tool", "golangci-lint", "run", "./..."}
	charms := map[string]spells.Charm{
		"rw":    {Ops: []spells.PatchOp{{Op: "add", Path: "/3", Value: "--fix"}}},
		"debug": {Ops: []spells.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
	}

	t.Run("one step per active charm, cumulative, sorted", func(t *testing.T) {
		steps, err := ExplainCharms(base, charms, []string{"debug", "rw"})
		require.NoError(t, err)
		require.Len(t, steps, 2)
		// Sorted name order: debug before rw.
		assert.Equal(t, "debug", steps[0].Charm)
		assert.Equal(t, []string{"tool", "golangci-lint", "run", "./...", "-v"}, steps[0].Command)
		assert.Equal(t, "rw", steps[1].Charm)
		assert.Equal(t, []string{"tool", "golangci-lint", "run", "--fix", "./...", "-v"}, steps[1].Command)
	})

	t.Run("undeclared or inactive charm contributes no step", func(t *testing.T) {
		steps, err := ExplainCharms(base, charms, []string{"rw", "nope"})
		require.NoError(t, err)
		require.Len(t, steps, 1)
		assert.Equal(t, "rw", steps[0].Charm)
	})

	t.Run("no active charms yields no steps", func(t *testing.T) {
		steps, err := ExplainCharms(base, charms, nil)
		require.NoError(t, err)
		assert.Empty(t, steps)
	})

	t.Run("a charm that does not apply is an error naming it", func(t *testing.T) {
		bad := map[string]spells.Charm{"oob": {Ops: []spells.PatchOp{{Op: "add", Path: "/99", Value: "x"}}}}
		_, err := ExplainCharms(base, bad, []string{"oob"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "oob")
	})
}

func TestApplyPatch(t *testing.T) {
	// applies asserts a patch produces the expected argv.
	applies := func(t *testing.T, argv []string, ops []spells.PatchOp, want []string) {
		t.Helper()
		got, err := ApplyPatch(argv, ops)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	// fails asserts a patch is rejected.
	fails := func(t *testing.T, argv []string, ops []spells.PatchOp) {
		t.Helper()
		_, err := ApplyPatch(argv, ops)
		assert.Error(t, err)
	}

	t.Run("no ops", func(t *testing.T) {
		applies(t, []string{"a", "b"}, nil, []string{"a", "b"})
	})
	t.Run("append end", func(t *testing.T) {
		applies(t, []string{"run", "./..."}, []spells.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}, []string{"run", "./...", "-v"})
	})
	t.Run("prepend", func(t *testing.T) {
		applies(t, []string{"build"}, []spells.PatchOp{{Op: "add", Path: "/0", Value: "-x"}}, []string{"-x", "build"})
	})
	t.Run("insert middle", func(t *testing.T) {
		applies(t, []string{"run", "./..."}, []spells.PatchOp{{Op: "add", Path: "/1", Value: "--fix"}}, []string{"run", "--fix", "./..."})
	})
	t.Run("replace element", func(t *testing.T) {
		applies(t, []string{"-l", "."}, []spells.PatchOp{{Op: "replace", Path: "/0", Value: "-w"}}, []string{"-w", "."})
	})
	t.Run("remove element", func(t *testing.T) {
		applies(t, []string{"mod", "tidy", "--diff"}, []spells.PatchOp{{Op: "remove", Path: "/2"}}, []string{"mod", "tidy"})
	})
	t.Run("two removes (rust fmt)", func(t *testing.T) {
		applies(t, []string{"fmt", "--", "--check"}, []spells.PatchOp{{Op: "remove", Path: "/2"}, {Op: "remove", Path: "/1"}}, []string{"fmt"})
	})
	t.Run("compose: insert then append", func(t *testing.T) {
		applies(t, []string{"run", "./..."}, []spells.PatchOp{{Op: "add", Path: "/1", Value: "--fix"}, {Op: "add", Path: "/-", Value: "-v"}}, []string{"run", "--fix", "./...", "-v"})
	})
	t.Run("move", func(t *testing.T) {
		applies(t, []string{"a", "b", "c"}, []spells.PatchOp{{Op: "move", Path: "/0", From: "/2"}}, []string{"c", "a", "b"})
	})
	t.Run("copy", func(t *testing.T) {
		applies(t, []string{"a", "b"}, []spells.PatchOp{{Op: "copy", Path: "/-", From: "/0"}}, []string{"a", "b", "a"})
	})
	t.Run("test pass", func(t *testing.T) {
		applies(t, []string{"go", "test"}, []spells.PatchOp{{Op: "test", Path: "/0", Value: "go"}}, []string{"go", "test"})
	})
	t.Run("test fail", func(t *testing.T) {
		fails(t, []string{"go", "test"}, []spells.PatchOp{{Op: "test", Path: "/0", Value: "rustc"}})
	})
	t.Run("index out of range", func(t *testing.T) {
		fails(t, []string{"a"}, []spells.PatchOp{{Op: "remove", Path: "/3"}})
	})
	t.Run("replace past end", func(t *testing.T) {
		fails(t, []string{"a"}, []spells.PatchOp{{Op: "replace", Path: "/1", Value: "x"}})
	})
	t.Run("add past end", func(t *testing.T) {
		fails(t, []string{"a"}, []spells.PatchOp{{Op: "add", Path: "/5", Value: "x"}})
	})
	t.Run("dash on remove invalid", func(t *testing.T) {
		fails(t, []string{"a"}, []spells.PatchOp{{Op: "remove", Path: "/-"}})
	})
	t.Run("leading zero invalid", func(t *testing.T) {
		fails(t, []string{"a", "b"}, []spells.PatchOp{{Op: "remove", Path: "/01"}})
	})

	// Input must never be mutated.
	base := []string{"a", "b"}
	_, err := ApplyPatch(base, []spells.PatchOp{{Op: "add", Path: "/-", Value: "c"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, base, "base mutated")
}

// TestApplyPatchConformance proves our flat-array applier agrees with the
// canonical RFC 6902 implementation (evanphx/json-patch) on the argv subset we
// use, so "follow the RFC" is verified, not just asserted.
func TestApplyPatchConformance(t *testing.T) {
	// agrees asserts our flat-array applier matches the reference impl.
	agrees := func(t *testing.T, argv []string, ops []spells.PatchOp) {
		t.Helper()
		got, err := ApplyPatch(argv, ops)
		require.NoError(t, err)
		ref, err := applyWithEvanphx(argv, ops)
		require.NoError(t, err)
		assert.Equal(t, ref, got)
	}

	agrees(t, []string{"run", "./..."}, []spells.PatchOp{{Op: "add", Path: "/-", Value: "-v"}})
	agrees(t, []string{"run", "./..."}, []spells.PatchOp{{Op: "add", Path: "/1", Value: "--fix"}})
	agrees(t, []string{"-l", "."}, []spells.PatchOp{{Op: "replace", Path: "/0", Value: "-w"}})
	agrees(t, []string{"mod", "tidy", "--diff"}, []spells.PatchOp{{Op: "remove", Path: "/2"}})
	agrees(t, []string{"fmt", "--", "--check"}, []spells.PatchOp{{Op: "remove", Path: "/2"}, {Op: "remove", Path: "/1"}})
	agrees(t, []string{"a", "b", "c"}, []spells.PatchOp{{Op: "move", Path: "/0", From: "/2"}})
	agrees(t, []string{"a", "b"}, []spells.PatchOp{{Op: "copy", Path: "/-", From: "/0"}})
	agrees(t, []string{"run", "./..."}, []spells.PatchOp{{Op: "add", Path: "/1", Value: "--fix"}, {Op: "add", Path: "/-", Value: "-v"}})
}

// applyWithEvanphx runs ops over argv via the reference RFC 6902 library by
// marshalling argv to a JSON array document and the ops to a JSON Patch.
func applyWithEvanphx(argv []string, ops []spells.PatchOp) ([]string, error) {
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
// a golden, keyed by runtime spell name (e.g. "go", not source dir "golang"). The
// built-in registry is produced by compiling each spells/<dir>/spell.buzz to bytecode
// and running it (see loadBuiltins); TestBuiltinsMatchGolden asserts that pipeline
// reproduces these exact values, so an accidental change in a built-in's .buzz — or in
// the Buzz toolchain — is caught. Update this table in lockstep with an intentional
// built-in change.
// Failure-advice tables, mirroring the shared lists the built-in .buzz sources declare.
// Named here for the same reason they are named there: the list is shared by several ops,
// so a golden that inlined it per op would drift one entry at a time.
var (
	goldenGoModHints = []spells.Hint{
		{Contains: "missing go.sum entry", Advise: "go.sum is out of date with go.mod; run `magus run generate:relock` (or `go mod tidy`) and commit the result"},
		{Contains: "updates to go.mod needed", Advise: "go.mod does not cover the imports in the tree; run `magus run generate:relock` (or `go mod tidy`) and commit the result"},
	}
	goldenPackageManagerHints = []spells.Hint{
		{Contains: "ERR_PNPM_OUTDATED_LOCKFILE", Advise: "pnpm-lock.yaml disagrees with package.json - usually a merge that changed one of them; run `pnpm install` and commit the lockfile"},
		{Contains: "ERR_PNPM_NO_SCRIPT", Advise: "package.json declares no such script; check the name, or the target may be pointed at the wrong project"},
		{Contains: "can only install packages when your package.json and package-lock.json", Advise: "package-lock.json disagrees with package.json - usually a merge that changed one of them; run `npm install` and commit the lockfile"},
		{Contains: "Missing script:", Advise: "package.json declares no such script; check the name, or the target may be pointed at the wrong project"},
		{Contains: "Your lockfile needs to be updated", Advise: "yarn.lock disagrees with package.json; run `yarn install` and commit the lockfile"},
		{Contains: "The lockfile would have been modified by this install", Advise: "yarn.lock disagrees with package.json; run `yarn install` and commit the lockfile"},
		{Contains: "lockfile had changes, but lockfile is frozen", Advise: "the bun lockfile disagrees with package.json; run `bun install` and commit the lockfile"},
		{Contains: "Cannot find module", Advise: "a dependency is missing from node_modules, usually a stale install; run your package manager's install"},
		{Contains: "command not found", Advise: "the tool is not in node_modules/.bin, usually a stale or missing install; run your package manager's install"},
	}
	goldenBuildxHints = []spells.Hint{
		{Contains: "denied: requested access to the resource is denied", Advise: "the registry accepted you but not this repository; check the image name and that the token may push to it"},
		{Contains: "authentication required", Advise: "not authenticated to the registry; run `docker login <registry>` and re-run (everything before the push replays from cache)"},
		{Contains: "unauthorized", Advise: "the registry rejected these credentials; run `docker login <registry>` and re-run, or check the token's scopes (a push needs write)"},
		{Contains: "Cannot connect to the Docker daemon", Advise: "the docker daemon is not running; start Docker Desktop or `systemctl start docker`"},
		{Contains: "Cannot connect to Podman", Advise: "the podman service is not running; `podman machine start` on macOS or Windows, or start the podman.socket unit on Linux"},
		{Contains: "authenticating creds for", Advise: "podman could not authenticate to the registry; run `podman login <registry>` and re-run"},
	}
	goldenRustupHints = []spells.Hint{
		{Contains: "no such command", Advise: "cargo subcommands like clippy and rustfmt are rustup components; install with `rustup component add clippy` (or rustfmt)"},
	}
	goldenPodmanHints = []spells.Hint{
		{Contains: "denied: requested access to the resource is denied", Advise: "the registry accepted you but not this repository; check the image name and that the token may push to it"},
		{Contains: "authenticating creds for", Advise: "podman could not authenticate to the registry; run `podman login <registry>` and re-run"},
		{Contains: "authentication required", Advise: "not authenticated to the registry; run `podman login <registry>` and re-run (everything before the push replays from cache)"},
		{Contains: "unauthorized", Advise: "the registry rejected these credentials; run `podman login <registry>` and re-run, or check the token's scopes (a push needs write)"},
		{Contains: "Cannot connect to Podman", Advise: "the podman service is not running; `podman machine start` on macOS or Windows, or start the podman.socket unit on Linux"},
		{Contains: "short-name resolution enforced but cannot prompt", Advise: "podman refuses ambiguous short names in non-interactive use; write the fully qualified image (docker.io/library/alpine, not alpine)"},
	}
)

var goldenBuiltins = map[string]spells.Descriptor{
	"bash": {
		Name:       "bash",
		Needs:      []string{"**/*.sh", "**/*.bash", ".shellcheckrc"},
		IgnoreDirs: []string{"node_modules", "worktrees"},
		Ops: map[string]spells.Op{
			"shellcheck": {Command: spells.Command{Bin: "shellcheck", Sources: []string{"**/*.sh", "**/*.bash"}}},
		},
	},
	"buf": {
		Name:     "buf",
		Needs:    []string{"**/*.proto", "buf.yaml", "buf.gen.yaml", "buf.work.yaml", "buf.lock"},
		Provides: []string{"gen/**"},
		Tools:    map[string]spells.Tool{"buf": {Probe: spells.Command{Bin: "buf", Args: []string{"--version"}}}},
		Ops: map[string]spells.Op{
			"buf-build":    {Command: spells.Command{Bin: "buf", Args: []string{"build"}}},
			"buf-generate": {Command: spells.Command{Bin: "buf", Args: []string{"generate"}}},
			"buf-lint": {Command: spells.Command{Bin: "buf", Args: []string{"lint"}, Charms: map[string]spells.Charm{
				"gha": {Ops: []spells.PatchOp{{Op: "add", Path: "/-", Value: "--error-format=github-actions"}}},
			}}},
			"buf-format": {Command: spells.Command{Bin: "buf", Args: []string{"format", "--exit-code"}, Charms: map[string]spells.Charm{
				"rw": {Ops: []spells.PatchOp{{Op: "replace", Path: "/1", Value: "-w"}}},
			}}},
			"buf-breaking": {Command: spells.Command{Bin: "buf", Args: []string{"breaking", "--against", ".git#branch=main"}, Charms: map[string]spells.Charm{
				"gha": {Ops: []spells.PatchOp{{Op: "add", Path: "/-", Value: "--error-format=github-actions"}}},
			}}},
		},
	},
	"buzz": {
		Name:  "buzz",
		Needs: []string{"**/*.buzz"},
		Ops: map[string]spells.Op{
			"buzz-check": {Command: spells.Command{Bin: "buzz", Args: []string{"--check"}, Sources: []string{"**/*.buzz"}, SourcesEach: true}},
			"buzz-test":  {Command: spells.Command{Bin: "buzz", Args: []string{"--test"}, Sources: []string{"**/*.buzz"}, SourcesEach: true}},
			"magus-buzz": {Command: spells.Command{Bin: "$MAGUS", Args: []string{"buzz"}, Sources: []string{"**/*.buzz"}, SourcesEach: true}},
		},
	},
	"cosign": {
		Name:  "cosign",
		Tools: map[string]spells.Tool{"cosign": {Probe: spells.Command{Bin: "cosign", Args: []string{"version"}}}},
		Ops: map[string]spells.Op{
			"cosign-sign":   {Command: spells.Command{Bin: "cosign", Args: []string{"sign", "--yes"}}},
			"cosign-verify": {Command: spells.Command{Bin: "cosign", Args: []string{"verify"}}},
			"cosign-attest": {Command: spells.Command{Bin: "cosign", Args: []string{"attest", "--yes"}}},
		},
	},
	"docker": {
		Name:  "docker",
		Needs: []string{"Dockerfile", ".dockerignore", "**/*"},
		Tools: map[string]spells.Tool{
			// `docker --version` is client-only; `docker info` detects a stopped daemon.
			"docker": {
				Probe: spells.Command{Bin: "docker", Args: []string{"--version"}},
				Key:   spells.VersionKey{UpTo: spells.VersionPatch},
				Ready: spells.Command{Bin: "docker", Args: []string{"info"}},
			},
			// hadolint is a second binary the spell drives, pinned by no manifest, so it
			// needs its own probe: upgrading it changes lint verdicts with nothing in any
			// cache key to notice. It gets no readiness probe - a lint talks to no daemon.
			"hadolint": {
				Probe:       spells.Command{Bin: "hadolint", Args: []string{"--version"}},
				Diagnostics: spells.DiagnosticGNU,
			},
		},
		Ops: map[string]spells.Op{
			"docker-build":       {Command: spells.Command{Bin: "docker", Args: []string{"build"}}},
			"docker-run":         {Command: spells.Command{Bin: "docker", Args: []string{"run", "--rm"}}},
			"docker-buildx":      {Command: spells.Command{Bin: "docker", Args: []string{"buildx", "build"}, Hints: goldenBuildxHints}},
			"docker-build-check": {Command: spells.Command{Bin: "docker", Args: []string{"build", "--check"}}},
			"hadolint":           {Command: spells.Command{Bin: "hadolint", Args: []string{"-f", "gnu", "Dockerfile"}}},
		},
	},
	"go": {
		Name:  "go",
		Needs: []string{"**/*.go", "**/*.txtar", "**/*.s", "**/*.S", "**/*.c", "**/*.h", "go.mod", "go.sum", "go.work", "go.work.sum"},
		Tools: map[string]spells.Tool{
			"go": {Probe: spells.Command{Bin: "go", Args: []string{"version"}}, Key: spells.VersionKey{UpTo: spells.VersionPatch},
				Supported: spells.VersionBounds{Min: "1.21"}},
			"golangci-lint": {Probe: spells.Command{Bin: "golangci-lint", Args: []string{"--version"}}, Key: spells.VersionKey{UpTo: spells.VersionPatch}},
			"govulncheck":   {Probe: spells.Command{Bin: "govulncheck", Args: []string{"-version"}}},
		},
		Language:   "go",
		IgnoreDirs: []string{"vendor"},
		Manifests:  []string{"go.mod"},
		Ops: map[string]spells.Op{
			"go-build":    {Command: spells.Command{Bin: "go", Args: []string{"build"}, Hints: goldenGoModHints}},
			"go-clean":    {Command: spells.Command{Bin: "go", Args: []string{"clean", "./..."}}},
			"go-generate": {Command: spells.Command{Bin: "go", Args: []string{"generate", "./..."}}},
			"go-mod-edit": {Command: spells.Command{Bin: "go", Args: []string{"mod", "edit", "-print"}, Capture: true, Charms: map[string]spells.Charm{
				"rw": {Ops: []spells.PatchOp{{Op: "remove", Path: "/2"}}},
			}}, Capture: true},
			"go-mod-json": {Command: spells.Command{Bin: "go", Args: []string{"mod", "edit", "-json"}, Capture: true}, Capture: true},
			"go-run":      {Command: spells.Command{Bin: "go", Args: []string{"run"}, Hints: goldenGoModHints}},
			"go-fmt": {Command: spells.Command{Bin: "gofmt", Args: []string{"-l", "."}, Charms: map[string]spells.Charm{
				"rw": {Ops: []spells.PatchOp{{Op: "replace", Path: "/0", Value: "-w"}}},
			}}},
			// Runs from PATH, not `go tool`: the module tool block never carried it, so
			// `go tool golangci-lint` reported "no such tool" and the op could not run.
			// Dropping the two-element prefix moves rw's insertion point from /3 to /1.
			"golangci-lint": {Command: spells.Command{Bin: "golangci-lint", Args: []string{"run", "./..."}, Charms: map[string]spells.Charm{
				"debug": {Ops: []spells.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
				"rw":    {Ops: []spells.PatchOp{{Op: "add", Path: "/1", Value: "--fix"}}},
			}}},
			"go-test": {Command: spells.Command{Bin: "go", Args: []string{"test", "./..."}, Hints: goldenGoModHints, Charms: map[string]spells.Charm{
				"debug": {Ops: []spells.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
				"cd": {Ops: []spells.PatchOp{
					{Op: "add", Path: "/-", Value: "-covermode=atomic"},
					{Op: "add", Path: "/-", Value: "-coverprofile=coverage.out"},
				}},
			}}},
			// tidy checks by default (--diff exits non-zero if go.mod/go.sum need
			// changes — safe for CI gating); relock applies the changes. relock rather
			// than rw because tidy re-resolves against the proxy.
			"go-mod-tidy": {Command: spells.Command{Bin: "go", Args: []string{"mod", "tidy", "--diff"}, Charms: map[string]spells.Charm{
				"relock": {Ops: []spells.PatchOp{{Op: "remove", Path: "/2"}}},
			}}},
			"go-vet":      {Command: spells.Command{Bin: "go", Args: []string{"vet", "./..."}}},
			"govulncheck": {Command: spells.Command{Bin: "govulncheck", Args: []string{"./..."}}},
			"scip":        {Command: spells.Command{Bin: "scip-go", Args: []string{"--output", "$MAGUS_SYMBOL_INDEX"}}},
		},
	},
	"markdown": {
		Name:  "markdown",
		Needs: []string{"**/*.md", "**/*.MD", "**/*.markdown", ".markdownlint.json", ".markdownlint.yaml", "dprint.json"},
		Ops: map[string]spells.Op{
			"dprint": {Command: spells.Command{Bin: "dprint", Args: []string{"check"}, Charms: map[string]spells.Charm{
				"rw": {Ops: []spells.PatchOp{{Op: "replace", Path: "/0", Value: "fmt"}}},
			}}},
			"markdownlint": {Command: spells.Command{Bin: "markdownlint", Args: []string{"**/*.md", "**/*.mdx"}}},
			"prettier": {Command: spells.Command{Bin: "prettier", Args: []string{"--check", "--no-error-on-unmatched-pattern", "**/*.md", "**/*.mdx"}, Charms: map[string]spells.Charm{
				"rw": {Ops: []spells.PatchOp{{Op: "replace", Path: "/0", Value: "--write"}}},
			}}},
			"typos": {Command: spells.Command{Bin: "typos", Args: []string{"--format", "brief"}, Charms: map[string]spells.Charm{
				"rw": {Ops: []spells.PatchOp{{Op: "add", Path: "/-", Value: "-w"}}},
			}}},
		},
	},
	"podman": {
		Name:  "podman",
		Needs: []string{"Containerfile", "Dockerfile", ".containerignore", ".dockerignore", "**/*"},
		Tools: map[string]spells.Tool{
			// Daemonless on Linux but VM-backed on macOS/Windows, so --version answers
			// whether the CLI exists and `info` whether it can do anything.
			"podman": {
				Probe: spells.Command{Bin: "podman", Args: []string{"--version"}},
				Key:   spells.VersionKey{UpTo: spells.VersionPatch},
				Ready: spells.Command{Bin: "podman", Args: []string{"info"}},
			},
		},
		Ops: map[string]spells.Op{
			"podman-build":    {Command: spells.Command{Bin: "podman", Args: []string{"build"}, Hints: goldenPodmanHints}},
			"podman-push":     {Command: spells.Command{Bin: "podman", Args: []string{"push"}, Hints: goldenPodmanHints}},
			"podman-manifest": {Command: spells.Command{Bin: "podman", Args: []string{"manifest"}, Hints: goldenPodmanHints}},
			"podman-run":      {Command: spells.Command{Bin: "podman", Args: []string{"run", "--rm"}, Hints: goldenPodmanHints}},
		},
	},
	"python": {
		Name:       "python",
		Needs:      []string{"**/*.py", "pyproject.toml", "requirements.txt", "requirements-*.txt", "Pipfile", "Pipfile.lock", "setup.py", "setup.cfg", "uv.lock", "poetry.lock"},
		Tools:      map[string]spells.Tool{"python3": {Probe: spells.Command{Bin: "python3", Args: []string{"--version"}}}},
		Language:   "python",
		IgnoreDirs: []string{"__pycache__"},
		Manifests:  []string{"pyproject.toml", "setup.py", "setup.cfg"},
		Ops: map[string]spells.Op{
			"uv-build": {Command: spells.Command{Bin: "uv", Args: []string{"build"}}},
			"uv-clean": {Command: spells.Command{Bin: "uv", Args: []string{"clean"}}},
			"pytest": {Command: spells.Command{Bin: "uv", Args: []string{"run", "pytest"}, Charms: map[string]spells.Charm{
				"debug": {Ops: []spells.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
			}}},
			"ruff-check": {Command: spells.Command{Bin: "uv", Args: []string{"run", "ruff", "check", "."}, Charms: map[string]spells.Charm{
				"debug": {Ops: []spells.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
				"rw":    {Ops: []spells.PatchOp{{Op: "add", Path: "/3", Value: "--fix"}}},
				"gha":   {Ops: []spells.PatchOp{{Op: "add", Path: "/3", Value: "--output-format=github"}}},
			}}},
			"ruff-format": {Command: spells.Command{Bin: "uv", Args: []string{"run", "ruff", "format", "--check", "."}, Charms: map[string]spells.Charm{
				"rw": {Ops: []spells.PatchOp{{Op: "remove", Path: "/3"}}},
			}}},
			"scip": {Command: spells.Command{Bin: "scip-python", Args: []string{"index", ".", "--output", "$MAGUS_SYMBOL_INDEX"}}},
		},
	},
	"rust": {
		Name:       "rust",
		Needs:      []string{"**/*.rs", "Cargo.toml", "Cargo.lock"},
		Tools:      map[string]spells.Tool{"rustc": {Probe: spells.Command{Bin: "rustc", Args: []string{"--version"}}, Key: spells.VersionKey{UpTo: spells.VersionPatch}}},
		Language:   "rust",
		IgnoreDirs: []string{"target"},
		Manifests:  []string{"Cargo.toml"},
		Ops: map[string]spells.Op{
			"cargo-build":  {Command: spells.Command{Bin: "cargo", Args: []string{"build", "--release"}}},
			"cargo-clean":  {Command: spells.Command{Bin: "cargo", Args: []string{"clean"}}},
			"cargo-clippy": {Command: spells.Command{Bin: "cargo", Args: []string{"clippy", "--", "-D", "warnings"}, Hints: goldenRustupHints}},
			"cargo-fmt": {Command: spells.Command{Bin: "cargo", Args: []string{"fmt", "--", "--check"}, Charms: map[string]spells.Charm{
				"rw": {Ops: []spells.PatchOp{{Op: "remove", Path: "/2"}, {Op: "remove", Path: "/1"}}},
			}}},
			"cargo-test": {Command: spells.Command{Bin: "cargo", Args: []string{"test"}}},
			"scip":       {Command: spells.Command{Bin: "rust-analyzer", Args: []string{"scip", ".", "--output", "$MAGUS_SYMBOL_INDEX"}}},
		},
	},
	"typescript": {
		Name:  "typescript",
		Needs: []string{"**/*.ts", "**/*.tsx", "**/*.mts", "**/*.cts", "**/*.js", "**/*.jsx", "**/*.mjs", "**/*.cjs", "**/*.json", "tsconfig*.json", "package.json", ".npmrc", "pnpm-lock.yaml", "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "bun.lockb"},
		// No Provides: tsc's output location is the project's tsconfig outDir, which the spell
		// cannot read, so it claims nothing rather than guessing "dist/**" (see MGS1018).
		Opaque: true,
		Tools: map[string]spells.Tool{
			"node": {Probe: spells.Command{Bin: "node", Args: []string{"--version"}}},
			"pnpm": {Probe: spells.Command{Bin: "pnpm", Args: []string{"--version"}},
				Key: spells.VersionKey{UpTo: spells.VersionPatch}},
			"tsc": {Probe: spells.Command{Bin: "pnpm", Args: []string{"exec", "tsc", "--version"}},
				Key: spells.VersionKey{UpTo: spells.VersionPatch}},
		},
		Language:   "typescript",
		IgnoreDirs: []string{"node_modules"},
		Manifests:  []string{"package.json"},
		Ops: map[string]spells.Op{
			"biome-check": {Command: spells.Command{Bin: "pnpm", Args: []string{"exec", "biome", "check", "."}, Hints: goldenPackageManagerHints, Charms: map[string]spells.Charm{
				"rw":  {Ops: []spells.PatchOp{{Op: "add", Path: "/3", Value: "--write"}}},
				"gha": {Ops: []spells.PatchOp{{Op: "add", Path: "/3", Value: "--reporter=github"}}},
			}}},
			"biome-format": {Command: spells.Command{Bin: "pnpm", Args: []string{"exec", "biome", "format", "."}, Hints: goldenPackageManagerHints, Charms: map[string]spells.Charm{
				"rw": {Ops: []spells.PatchOp{{Op: "add", Path: "/3", Value: "--write"}}},
			}}},
			"eslint": {Command: spells.Command{Bin: "pnpm", Args: []string{"exec", "eslint", "."}, Hints: goldenPackageManagerHints, Charms: map[string]spells.Charm{
				"rw":  {Ops: []spells.PatchOp{{Op: "add", Path: "/2", Value: "--fix"}}},
				"gha": {Ops: []spells.PatchOp{{Op: "add", Path: "/2", Value: "--format=unix"}}},
			}}},
			"preflight": {},
			"prettier": {Command: spells.Command{Bin: "pnpm", Args: []string{"exec", "prettier", "--check", "."}, Hints: goldenPackageManagerHints, Charms: map[string]spells.Charm{
				"rw": {Ops: []spells.PatchOp{{Op: "replace", Path: "/2", Value: "--write"}}},
			}}},
			"tsc":       {Command: spells.Command{Bin: "pnpm", Args: []string{"exec", "tsc"}, Hints: goldenPackageManagerHints}},
			"tsc-build": {Command: spells.Command{Bin: "pnpm", Args: []string{"exec", "tsc", "--build"}}},
			"tsc-clean": {Command: spells.Command{Bin: "pnpm", Args: []string{"exec", "tsc", "--build", "--clean"}}},
			"vitest": {Command: spells.Command{Bin: "pnpm", Args: []string{"exec", "vitest", "run"}, Charms: map[string]spells.Charm{
				"gha": {Ops: []spells.PatchOp{{Op: "add", Path: "/-", Value: "--reporter=github-actions"}}},
			}}},
			"dev-server": {Kind: "service", Command: spells.Command{Bin: "pnpm", Args: []string{"run", "dev"}}, Service: &spells.Service{
				Command: spells.Command{Bin: "pnpm", Args: []string{"run", "dev"}},
			}},
			"scip": {Command: spells.Command{Bin: "scip-typescript", Args: []string{"index", "--output", "$MAGUS_SYMBOL_INDEX"}}},
		},
	},
}

func TestBuiltinsMatchGolden(t *testing.T) {
	got := Builtins()
	require.Len(t, got, len(goldenBuiltins), "registry/golden size mismatch")
	for name, want := range goldenBuiltins {
		g, ok := got[name]
		if !assert.Truef(t, ok, "registry missing built-in %q", name) {
			continue
		}
		// DocOps and each op's Doc are resolution-path metadata (which targets are
		// function handlers, and their doc comments), not part of a spell's semantic
		// identity — and the Doc is not even stable across compiler versions (whether
		// bytecode serializes doc comments varies). Clear both before comparing the
		// semantic fields (bin/args/charms).
		g.DocOps = nil
		for opName, op := range g.Ops {
			op.Doc = ""
			g.Ops[opName] = op
		}
		assert.Equalf(t, want, g, "built-in %q", name)
	}
}

// TestConflicts covers the overridden-charm detector: a destructive overlap on the
// same argv position is a conflict (the alphabetically-later charm wins), while
// disjoint edits, a lone charm, and an inert charm are not.
func TestConflicts(t *testing.T) {
	base := []string{"lint", "."}
	charms := map[string]spells.Charm{
		// two charms fight over /0; "rw" sorts after "fmt", so rw wins and fmt is lost.
		"fmt": {Ops: []spells.PatchOp{{Op: "replace", Path: "/0", Value: "format"}}},
		"rw":  {Ops: []spells.PatchOp{{Op: "replace", Path: "/0", Value: "fix"}}},
		// disjoint appends: both survive regardless of order.
		"debug": {Ops: []spells.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
		"trace": {Ops: []spells.PatchOp{{Op: "add", Path: "/-", Value: "-x"}}},
		// inert: declares no ops.
		"noop": {},
	}

	t.Run("destructive overlap is a conflict", func(t *testing.T) {
		got, err := Conflicts(base, charms, []string{"rw", "fmt"})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "fmt", got[0].Name, "the losing charm is reported")
		assert.Equal(t, "rw", got[0].OverriddenBy, "the winner is named")
	})

	t.Run("disjoint appends do not conflict", func(t *testing.T) {
		got, err := Conflicts(base, charms, []string{"debug", "trace"})
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("disjoint positions do not conflict", func(t *testing.T) {
		got, err := Conflicts(base, charms, []string{"rw", "debug"})
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("a single charm cannot conflict", func(t *testing.T) {
		got, err := Conflicts(base, charms, []string{"rw"})
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("an inert charm is not a conflict", func(t *testing.T) {
		got, err := Conflicts(base, charms, []string{"noop", "rw"})
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}
