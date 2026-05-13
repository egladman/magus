package playground

// builtinSpellOps maps a built-in spell's import name (`import "magus/spell/go"`
// binds `go`) to the operation names a magusfile invokes on it
// (`go["go-build"]()`). It mirrors magus/internal/spell's built-in registry; the
// host-only TestManifestMatchesBuiltins gate keeps it from drifting out of sync
// with the real spells. It is a hand-written manifest rather than generated
// because the playground wasm must not pull in the spell package's embedded
// bytecode and codec — only these names are needed to build the recording stubs.
//
// This is a representative subset of the registry (the spells a magusfile is
// most likely to bind), not the full set; add a row here when the playground
// needs another spell.
var builtinSpellOps = map[string][]string{
	"go":      {"go-build", "go-test", "go-vet", "go-fmt", "go-mod-tidy", "go-generate", "go-clean", "golangci-lint", "govulncheck"},
	"rs":      {"cargo-build", "cargo-test", "cargo-clippy", "cargo-fmt", "cargo-clean"},
	"py":      {"pytest", "ruff-check", "ruff-format", "uv-build", "uv-clean"},
	"js":      {"eslint", "prettier", "vitest", "preflight"},
	"ts":      {"tsc", "eslint", "prettier", "vitest", "preflight"},
	"docker":  {"docker-build", "docker-build-check", "hadolint"},
	"compose": {"docker-compose-build", "docker-compose-config"},
	"buf":     {"buf-build", "buf-lint", "buf-format", "buf-generate"},
	"buzz":    {"buzz-check", "buzz-test", "magus-buzz"},
	"zig":     {"zig-build", "zig-build-test", "zig-fmt", "clean"},
	"yaml":    {"yamlfmt", "yamllint"},
	"md":      {"markdownlint", "prettier"},
}
