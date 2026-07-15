package dry

// builtinSpellOps maps a built-in spell's import name (`import "magus/spell/go"`
// binds `go`) to the operation names a magusfile invokes on it
// (`go["go-build"]()`). It mirrors internal/spell's full built-in registry so every
// spell's docs example is runnable-as-dry-run (the spell-docs Run button dispatches
// through these stubs); the host-only TestManifestMatchesBuiltins gate keeps it from
// drifting. It is hand-written rather than generated because the playground wasm must
// not pull in the spell package's embedded bytecode and codec - only these names are
// needed to build the tracing stubs. Add a row (and its ops) when a new built-in
// spell ships.
var builtinSpellOps = map[string][]string{
	"go":     {"go-build", "go-test", "go-vet", "go-fmt", "go-mod-tidy", "go-generate", "go-clean", "golangci-lint", "govulncheck"},
	"rs":     {"cargo-build", "cargo-test", "cargo-clippy", "cargo-fmt", "cargo-clean"},
	"py":     {"pytest", "ruff-check", "ruff-format", "uv-build", "uv-clean"},
	"ts":     {"tsc", "tsc-build", "tsc-clean", "eslint", "prettier", "vitest", "preflight", "dev-server"},
	"docker": {"docker-build", "docker-build-check", "docker-buildx", "hadolint"},
	"buf":    {"buf-build", "buf-lint", "buf-format", "buf-generate"},
	"buzz":   {"buzz-check", "buzz-test", "magus-buzz"},
	"md":     {"markdownlint", "prettier"},
	"cosign": {"cosign-sign", "cosign-attest", "cosign-verify"},
	"bash":   {"shellcheck"},
}
