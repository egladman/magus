# CLAUDE.md

magus is a monorepo build/task tool (Go 1.25, module `github.com/egladman/magus`).
Users declare targets in `magusfile.buzz` (Buzz, an embedded scripting language);
magus resolves spells/ops, sandboxes execution, and caches results. This repo
builds itself with magus - see `magusfile.buzz` at the root.

Start with `MAGUS.md`: the generated target catalog and entry point. Do not
hand-edit it.

## Commands

- Build: `magus run build`
- Test: `magus run test` (or `go test ./...`)
- Lint + vet + vuln: `magus run lint`
- Final gate before handing work back: `magus affected ci` - runs the full
  pipeline (lint, build, test, coverage) over affected projects.
- Regenerate generated files: `magus run generate`, then commit.
- Charm note: `magus.yaml` sets `default_charms: [rw]`, so local runs get
  the `rw` charm automatically - `generate` writes locally. CI strips it
  (`--no-default-charms` / the ci anchor), where `generate` acts as a pure
  drift gate instead.

## Which magus binary (dogfood convention)

Use the released `magus` on PATH by default; drop to source only when your
change is to magus itself.

- Default: the installed release binary (`magus ...`). CI does the same:
  `setup-magus` downloads the pinned release, checksum-verified, and only
  falls back to `go build ./cmd/magus`.
- Testing changes to magus itself: `go run ./cmd/magus run <target>` (or
  `go build -o /tmp/magus ./cmd/magus`) to exercise HEAD explicitly.
- Why: the released binary building HEAD is the compatibility contract -
  if `magusfile.buzz` ever needs an unreleased magus feature, that is a
  breaking-change signal to surface (release first), not to paper over by
  quietly switching to `go run`.

## Layout

- `magus.go` + root `*.go` - public API and composition root (`Open`, `Inspect`)
- `types/` - pure domain types, stdlib-only; keep it a leaf
- `internal/` - the engine (cache, interp, depgraph, spell, proc, sandbox, ...)
- `cmd/magus` - the CLI; `cmd/magus-*` - codegen and docs tools
- `std/` + `host/` - Buzz stdlib modules; `host/gen/` is generated from `std/`
- `gopherbuzz/` - the embedded Buzz language implementation
- `spells/` - built-in spell sources (`.buzz`), compiled into the binary
- `docs/` - markdown sources; `website/scribe.buzz` renders them into the
  committed static site at `website/gen/`

## Rules

- No emojis anywhere: code, output, commits, docs.
- User-facing message strings are plain ASCII (no em-dashes, curly quotes);
  code comments are exempt. Docs frontmatter is plain ASCII too.
- Never hand-edit generated files (`gen/` dirs, `*.gen.buzz`, `MAGUS.md`,
  `website/gen/`); change the source of truth and regenerate.
- Website follows classless Pico: semantic HTML, minimal custom classes,
  no inline styles.
- Language-level changes in `gopherbuzz/` must match upstream Buzz behavior.
- Buzz code is tested with in-file `test "..." {}` blocks; run via
  `magus buzz -t <file>`.
- Commits: subject line only, no area prefix, no Co-Authored-By trailer;
  join multiple ideas with semicolons. Never push unless explicitly asked.
