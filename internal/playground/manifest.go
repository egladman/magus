package playground

// builtinSpellOps maps a built-in spell's import name (`import "magus/spell/go"`
// binds `go`) to the operation names a magusfile invokes on it
// (`go["go-build"]()`). It mirrors internal/spell's built-in registry; the
// host-only TestManifestMatchesBuiltins gate keeps it from drifting out of sync
// with the real spells. It is a hand-written manifest rather than generated
// because the playground wasm must not pull in the spell package's embedded
// bytecode and codec — only these names are needed to build the recording stubs.
//
// This mirrors the full built-in registry so every spell's docs example is
// runnable-as-dry-run (the spell-docs Run button dispatches through these stubs);
// the host-only TestManifestMatchesBuiltins gate keeps it from drifting. Add a
// row (and its ops) here when a new built-in spell ships.
var builtinSpellOps = map[string][]string{
	"go":     {"go-build", "go-test", "go-vet", "go-fmt", "go-mod-tidy", "go-generate", "go-clean", "golangci-lint", "govulncheck"},
	"rs":     {"cargo-build", "cargo-test", "cargo-clippy", "cargo-fmt", "cargo-clean"},
	"py":     {"pytest", "ruff-check", "ruff-format", "uv-build", "uv-clean"},
	"ts":     {"tsc", "eslint", "prettier", "vitest", "preflight"},
	"docker": {"docker-build", "docker-build-check", "docker-buildx", "hadolint"},
	"buf":    {"buf-build", "buf-lint", "buf-format", "buf-generate"},
	"buzz":   {"buzz-check", "buzz-test", "magus-buzz"},
	"md":     {"markdownlint", "prettier"},
	"cosign": {"cosign-sign", "cosign-attest", "cosign-verify"},
	"bash":   {"shellcheck"},
}
