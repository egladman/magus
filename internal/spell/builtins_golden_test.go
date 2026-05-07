package spell

import (
	"reflect"
	"testing"
)

// goldenBuiltins is the original hand-maintained built-in table, frozen here as
// a golden. The built-in registry is now produced by compiling each
// magus/spells/<name>/spell.bzz to bytecode and running it (see loadBuiltins);
// TestBuiltinsMatchGolden asserts that pipeline reproduces these exact values,
// so an accidental change in a built-in's .bzz — or in the Buzz toolchain — is
// caught. Update this table in lockstep with an intentional built-in change.
var goldenBuiltins = map[string]Spec{
	"bash": {
		Name:  "bash",
		Needs: []string{"**/*.sh", "**/*.bash", ".shellcheckrc"},
		Targets: map[string]Target{
			"shellcheck": {Cmd: "sh", Args: []string{"-c", "find . \\( -name '*.sh' -o -name '*.bash' \\) -print0 | xargs -0 -r shellcheck"}},
		},
	},
	"buf": {
		Name:       "buf",
		Needs:      []string{"**/*.proto", "buf.yaml", "buf.gen.yaml", "buf.work.yaml", "buf.lock"},
		Provides:   []string{"gen/**"},
		VersionCmd: []string{"buf", "--version"},
		Targets: map[string]Target{
			"buf-build":    {Cmd: "buf", Args: []string{"build"}},
			"buf-generate": {Cmd: "buf", Args: []string{"generate"}},
			"buf-lint": {Cmd: "buf", Args: []string{"lint"}, Charms: map[string]Charm{
				"gha": {Ops: []PatchOp{{Op: "add", Path: "/-", Value: "--error-format=github-actions"}}},
			}},
			"buf-format": {Cmd: "buf", Args: []string{"format", "--exit-code"}, Charms: map[string]Charm{
				"rw": {Ops: []PatchOp{{Op: "replace", Path: "/1", Value: "-w"}}},
			}},
		},
	},
	"buzz": {
		Name:  "buzz",
		Needs: []string{"**/*.buzz", "**/*.bzz"},
		Targets: map[string]Target{
			"buzz-check": {Cmd: "sh", Args: []string{"-c", "find . \\( -name '*.buzz' -o -name '*.bzz' \\) -print0 | xargs -0 -r -n1 buzz --check"}},
			"buzz-test":  {Cmd: "sh", Args: []string{"-c", "find . \\( -name '*.buzz' -o -name '*.bzz' \\) -print0 | xargs -0 -r -n1 buzz --test"}},
			"magus-buzz": {Cmd: "sh", Args: []string{"-c", "find . \\( -name '*.buzz' -o -name '*.bzz' \\) -print0 | xargs -0 -r -n1 \"$MAGUS\" buzz"}},
		},
	},
	"compose": {
		Name:  "compose",
		Needs: []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml", ".dockerignore", "Dockerfile", "**/Dockerfile"},
		Targets: map[string]Target{
			"docker-compose-build":  {Cmd: "docker", Args: []string{"compose", "build"}},
			"docker-compose-config": {Cmd: "docker", Args: []string{"compose", "config", "--quiet"}},
		},
	},
	"css": {
		Name:   "css",
		Needs:  []string{"**/*.css", "**/*.scss", "**/*.sass", "**/*.less", ".stylelintrc", ".stylelintrc.json", ".stylelintrc.yaml"},
		Claims: []string{"**/*.css", "**/*.scss", "**/*.sass", "**/*.less"},
		Targets: map[string]Target{
			"prettier": {Cmd: "prettier", Args: []string{"--check", "**/*.css", "**/*.scss", "**/*.sass", "**/*.less"}, Charms: map[string]Charm{
				"rw": {Ops: []PatchOp{{Op: "replace", Path: "/0", Value: "--write"}}},
			}},
			"stylelint": {Cmd: "stylelint", Args: []string{"**/*.css", "**/*.scss", "**/*.sass", "**/*.less"}},
		},
	},
	"docker": {
		Name:       "docker",
		Needs:      []string{"Dockerfile", ".dockerignore", "**/*"},
		VersionCmd: []string{"docker", "--version"},
		Targets: map[string]Target{
			"docker-build":       {Cmd: "docker", Args: []string{"build"}},
			"docker-build-check": {Cmd: "docker", Args: []string{"build", "--check"}},
			"hadolint":           {Cmd: "hadolint", Args: []string{"Dockerfile"}},
		},
	},
	"golang": {
		Name:       "go",
		Needs:      []string{"**/*.go", "go.mod", "go.sum", "go.work", "go.work.sum"},
		VersionCmd: []string{"go", "version"},
		Targets: map[string]Target{
			"go-build":    {Cmd: "go", Args: []string{"build"}},
			"go-clean":    {Cmd: "go", Args: []string{"clean", "./..."}},
			"go-generate": {Cmd: "go", Args: []string{"generate", "./..."}},
			"go-fmt": {Cmd: "gofmt", Args: []string{"-l", "."}, Charms: map[string]Charm{
				"rw": {Ops: []PatchOp{{Op: "replace", Path: "/0", Value: "-w"}}},
			}},
			"golangci-lint": {Cmd: "go", Args: []string{"tool", "golangci-lint", "run", "./..."}, Charms: map[string]Charm{
				"debug": {Ops: []PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
				"rw":    {Ops: []PatchOp{{Op: "add", Path: "/3", Value: "--fix"}}},
			}},
			"go-test": {Cmd: "go", Args: []string{"test", "./..."}, Charms: map[string]Charm{
				"debug": {Ops: []PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
				"cd": {Ops: []PatchOp{
					{Op: "add", Path: "/-", Value: "-covermode=atomic"},
					{Op: "add", Path: "/-", Value: "-coverprofile=coverage.out"},
				}},
			}},
			// tidy checks by default (--diff exits non-zero if go.mod/go.sum need
			// changes — safe for CI gating); the write charm applies the changes.
			"go-mod-tidy": {Cmd: "go", Args: []string{"mod", "tidy", "--diff"}, Charms: map[string]Charm{
				"rw": {Ops: []PatchOp{{Op: "remove", Path: "/2"}}},
			}},
			"go-vet":      {Cmd: "go", Args: []string{"vet", "./..."}},
			"govulncheck": {Cmd: "go", Args: []string{"tool", "govulncheck", "./..."}},
		},
	},
	"html": {
		Name:   "html",
		Needs:  []string{"**/*.html", "**/*.htm", ".htmlhintrc"},
		Claims: []string{"**/*.html", "**/*.htm"},
		Targets: map[string]Target{
			"htmlhint": {Cmd: "htmlhint", Args: []string{"**/*.html", "**/*.htm"}},
			"prettier": {Cmd: "prettier", Args: []string{"--check", "**/*.html", "**/*.htm"}, Charms: map[string]Charm{
				"rw": {Ops: []PatchOp{{Op: "replace", Path: "/0", Value: "--write"}}},
			}},
		},
	},
	"javascript": {
		Name:       "js",
		Needs:      []string{"**/*.js", "**/*.mjs", "**/*.cjs", "**/*.jsx", "package.json", ".npmrc", "pnpm-lock.yaml", "package-lock.json"},
		Claims:     []string{"**/*.js", "**/*.mjs", "**/*.cjs", "**/*.jsx", "**/*.json", "**/*.jsonc", "**/*.md", "**/*.mdx", "**/*.yaml", "**/*.yml", "**/*.css", "**/*.scss", "**/*.html"},
		Opaque:     true,
		VersionCmd: []string{"node", "--version"},
		Targets: map[string]Target{
			"eslint":    {Cmd: "pnpm", Args: []string{"exec", "eslint", "."}},
			"preflight": {},
			"prettier": {Cmd: "pnpm", Args: []string{"exec", "prettier", "--check", "."}, Charms: map[string]Charm{
				"rw": {Ops: []PatchOp{{Op: "replace", Path: "/2", Value: "--write"}}},
			}},
			"vitest": {Cmd: "pnpm", Args: []string{"exec", "vitest", "run"}, Charms: map[string]Charm{
				"gha": {Ops: []PatchOp{{Op: "add", Path: "/-", Value: "--reporter=github-actions"}}},
			}},
		},
	},
	"json": {
		Name:   "json",
		Needs:  []string{"**/*.json", "**/*.jsonc", "biome.json", ".prettierrc.json"},
		Claims: []string{"**/*.json", "**/*.jsonc"},
		Targets: map[string]Target{
			"prettier": {Cmd: "prettier", Args: []string{"--check", "**/*.json", "**/*.jsonc"}, Charms: map[string]Charm{
				"rw": {Ops: []PatchOp{{Op: "replace", Path: "/0", Value: "--write"}}},
			}},
		},
	},
	"kind": {
		Name:  "kind",
		Needs: []string{"kind-cluster.yaml"},
		Targets: map[string]Target{
			"kind-create-cluster": {Cmd: "sh", Args: []string{"-c", "kind create cluster --name \"$(basename \"$PWD\")\" --config kind-cluster.yaml"}},
			"kind-delete-cluster": {Cmd: "sh", Args: []string{"-c", "kind delete cluster --name \"$(basename \"$PWD\")\""}},
		},
	},
	"kustomize": {
		Name:  "kustomize",
		Needs: []string{"kustomization.yaml", "kustomization.yml", "Kustomization", "**/*.yaml", "**/*.yml"},
		Targets: map[string]Target{
			"kustomize-build": {Cmd: "kustomize", Args: []string{"build", "."}},
		},
	},
	"markdown": {
		Name:   "md",
		Needs:  []string{"**/*.md", "**/*.MD", "**/*.markdown", ".markdownlint.json", ".markdownlint.yaml"},
		Claims: []string{"**/*.md", "**/*.mdx"},
		Targets: map[string]Target{
			"markdownlint": {Cmd: "markdownlint", Args: []string{"**/*.md", "**/*.mdx"}},
			"prettier": {Cmd: "prettier", Args: []string{"--check", "--no-error-on-unmatched-pattern", "**/*.md", "**/*.mdx"}, Charms: map[string]Charm{
				"rw": {Ops: []PatchOp{{Op: "replace", Path: "/0", Value: "--write"}}},
			}},
		},
	},
	"python": {
		Name:       "py",
		Needs:      []string{"**/*.py", "pyproject.toml", "requirements.txt", "requirements-*.txt", "Pipfile", "Pipfile.lock", "setup.py", "setup.cfg", "uv.lock", "poetry.lock"},
		VersionCmd: []string{"python3", "--version"},
		Targets: map[string]Target{
			"uv-build": {Cmd: "uv", Args: []string{"build"}},
			"uv-clean": {Cmd: "uv", Args: []string{"clean"}},
			"pytest": {Cmd: "uv", Args: []string{"run", "pytest"}, Charms: map[string]Charm{
				"debug": {Ops: []PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
			}},
			"ruff-check": {Cmd: "uv", Args: []string{"run", "ruff", "check", "."}, Charms: map[string]Charm{
				"debug": {Ops: []PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
				"rw":    {Ops: []PatchOp{{Op: "add", Path: "/3", Value: "--fix"}}},
				"gha":   {Ops: []PatchOp{{Op: "add", Path: "/3", Value: "--output-format=github"}}},
			}},
			"ruff-format": {Cmd: "uv", Args: []string{"run", "ruff", "format", "--check", "."}, Charms: map[string]Charm{
				"rw": {Ops: []PatchOp{{Op: "remove", Path: "/3"}}},
			}},
		},
	},
	"rust": {
		Name:       "rs",
		Needs:      []string{"**/*.rs", "Cargo.toml", "Cargo.lock"},
		VersionCmd: []string{"rustc", "--version"},
		Targets: map[string]Target{
			"cargo-build":  {Cmd: "cargo", Args: []string{"build", "--release"}},
			"cargo-clean":  {Cmd: "cargo", Args: []string{"clean"}},
			"cargo-clippy": {Cmd: "cargo", Args: []string{"clippy", "--", "-D", "warnings"}},
			"cargo-fmt": {Cmd: "cargo", Args: []string{"fmt", "--", "--check"}, Charms: map[string]Charm{
				"rw": {Ops: []PatchOp{{Op: "remove", Path: "/2"}, {Op: "remove", Path: "/1"}}},
			}},
			"cargo-test": {Cmd: "cargo", Args: []string{"test"}},
		},
	},
	"sql": {
		Name:  "sql",
		Needs: []string{"**/*.sql", "**/*.SQL", ".sqlfluff"},
		Targets: map[string]Target{
			"sqlfluff": {Cmd: "sqlfluff", Args: []string{"lint", "."}, Charms: map[string]Charm{
				"rw":  {Ops: []PatchOp{{Op: "replace", Path: "/0", Value: "fix"}}},
				"gha": {Ops: []PatchOp{{Op: "add", Path: "/1", Value: "--format=github-annotation-native"}}},
			}},
		},
	},
	"terraform": {
		Name:       "tf",
		Needs:      []string{"**/*.tf", "**/*.tfvars", "**/*.tf.json", ".terraform.lock.hcl"},
		VersionCmd: []string{"terraform", "version"},
		Targets: map[string]Target{
			"terraform-fmt": {Cmd: "terraform", Args: []string{"fmt", "-check", "-recursive", "."}, Charms: map[string]Charm{
				"rw": {Ops: []PatchOp{{Op: "remove", Path: "/1"}}},
			}},
		},
	},
	"toml": {
		Name:  "toml",
		Needs: []string{"**/*.toml", "taplo.toml", ".taplo.toml"},
		Targets: map[string]Target{
			"taplo": {Cmd: "taplo", Args: []string{"check"}, Charms: map[string]Charm{
				"rw": {Ops: []PatchOp{{Op: "replace", Path: "/0", Value: "fmt"}}},
			}},
		},
	},
	"typescript": {
		Name:       "ts",
		Needs:      []string{"**/*.ts", "**/*.tsx", "**/*.js", "**/*.jsx", "**/*.json", "package.json", ".npmrc", "pnpm-lock.yaml", "package-lock.json", "npm-shrinkwrap.json"},
		Claims:     []string{"**/*.ts", "**/*.tsx", "**/*.mts", "**/*.cts", "**/*.js", "**/*.mjs", "**/*.cjs", "**/*.jsx", "**/*.json", "**/*.jsonc", "**/*.md", "**/*.mdx", "**/*.yaml", "**/*.yml", "**/*.css", "**/*.scss", "**/*.html"},
		Opaque:     true,
		VersionCmd: []string{"node", "--version"},
		Targets: map[string]Target{
			"eslint":    {Cmd: "pnpm", Args: []string{"exec", "eslint", "."}},
			"preflight": {},
			"prettier": {Cmd: "pnpm", Args: []string{"exec", "prettier", "--check", "."}, Charms: map[string]Charm{
				"rw": {Ops: []PatchOp{{Op: "replace", Path: "/2", Value: "--write"}}},
			}},
			"tsc": {Cmd: "pnpm", Args: []string{"exec", "tsc"}},
			"vitest": {Cmd: "pnpm", Args: []string{"exec", "vitest", "run"}, Charms: map[string]Charm{
				"gha": {Ops: []PatchOp{{Op: "add", Path: "/-", Value: "--reporter=github-actions"}}},
			}},
		},
	},
	"yaml": {
		Name:   "yaml",
		Needs:  []string{"**/*.yaml", "**/*.yml", ".yamllint", ".yamllint.yaml"},
		Claims: []string{"**/*.yaml", "**/*.yml"},
		Targets: map[string]Target{
			"yamlfmt": {Cmd: "yamlfmt", Args: []string{"-lint", "."}, Charms: map[string]Charm{
				"rw": {Ops: []PatchOp{{Op: "remove", Path: "/0"}}},
			}},
			"yamllint": {Cmd: "yamllint", Args: []string{"."}},
		},
	},
	"zig": {
		Name:       "zig",
		Needs:      []string{"**/*.zig", "**/*.zon", "build.zig", "build.zig.zon"},
		VersionCmd: []string{"zig", "version"},
		Targets: map[string]Target{
			"zig-build": {Cmd: "zig", Args: []string{"build"}},
			"clean":     {Cmd: "rm", Args: []string{"-rf", "zig-out", ".zig-cache"}},
			"zig-fmt": {Cmd: "zig", Args: []string{"fmt", "--check", "."}, Charms: map[string]Charm{
				"rw": {Ops: []PatchOp{{Op: "remove", Path: "/1"}}},
			}},
			"zig-build-test": {Cmd: "zig", Args: []string{"build", "test"}},
		},
	},
}

func TestBuiltinsMatchGolden(t *testing.T) {
	got := builtinsByDir()
	if len(got) != len(goldenBuiltins) {
		t.Fatalf("registry has %d built-ins, golden has %d", len(got), len(goldenBuiltins))
	}
	for dir, want := range goldenBuiltins {
		g, ok := got[dir]
		if !ok {
			t.Errorf("registry missing built-in %q", dir)
			continue
		}
		// DocTargets is resolution-path metadata (which targets are function
		// handlers), not part of a spell's semantic identity, and is not pinned by
		// this golden; clear it before comparing the semantic fields.
		g.DocTargets = nil
		if !reflect.DeepEqual(g, want) {
			t.Errorf("built-in %q:\n got: %#v\nwant: %#v", dir, g, want)
		}
	}
}
