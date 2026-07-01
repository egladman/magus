# magus/std - the host-binding superset

This package is magus's **host API**: the modules a `magusfile.buzz` (or a spell)
calls into to touch the outside world - run processes, read files, query the VCS,
make HTTP requests, hash, (de)serialize, build charm patches. It is layered on
top of [`gopherbuzz/std`](../gopherbuzz/std/README.md) (the Buzz language stdlib)
to form one **superset** surface.

## How it relates to `gopherbuzz/std`

`gopherbuzz/std` is the portable Buzz standard library and must match upstream
Buzz. `magus/std` is everything magus adds on top:

```text
import "os"  in a magusfile
  ├── os.sleep / os.time / os.env / os.execute / …   ← gopherbuzz/std (the language)
  └── os.exec / os.which / os.retry / os.with_env / …← magus/std (this package)
```

- **Layered onto the same bare names.** magus registers `gopherbuzz/std` first,
  then overlays its host methods onto the same modules (`os`, `fs`, `crypto`, …),
  so `import "os"` carries both. magus wins on the few shared keys because its
  forms are sandbox- and context-aware (the wiring lives in
  `internal/interp/bindings`, which is shared by the magusfile and spell paths).
- **Plus modules Buzz has no concept of:** `vcs`, `archive`, `http`, `env`,
  `time`, `fmt`, `markdown`, `charm`, `encoding`, `path`, `strings`, `semver`,
  `yaml`, `platform`, and the `magus` core namespace.
- The native-equivalent cross-reference (which host method duplicates a Buzz
  stdlib call) is in [`../host/overlap.go`](../host/overlap.go).

Anything magus-specific goes **here**, never in `gopherbuzz/std` - that package
stays upstream-shaped so standalone Buzz programs remain portable.

## The native binding mechanism

A host module is declared **once**, as a `Module` value with typed `Args`,
`Returns`, and a Go `Impl` (see [`module.go`](module.go)):

```go
var Os = Module{
    Name: "os",
    Methods: []Method{{
        Name: "which", Args: []Arg{{Name: "cmd", Type: TypeString}},
        Returns: []Ret{{Type: TypeString}}, Impl: OsWhich,
    }},
}
func init() { Register(Os) }
```

`magus-utils bindings` reflects over each `Impl` and emits the Buzz trampoline into
the sibling [`../host/gen`](../host) package (`//go:generate` lives next to
each descriptor in `std/*.go`); a drift test keeps the generated files in lockstep
with the declarations. This is the single source of truth: declare the typed
signature, regenerate, and the docs (`magus describe module`, `docs/modules/*.md`)
and the Buzz binding both follow. Adding a module is one descriptor + `Register`
in `init()` + a `//go:generate` line.

`std` itself stays VM-agnostic: the Buzz value marshalling and the trampolines
live in [`../host`](../host), and the few byte-level companions that can't
be declared (the `crypto.hmacSha256` / `http.download` family, whose args cross
as `[int]` byte lists the `TypeTag` set can't express) are hand-written VM glue in
`internal/interp/bindings`.

## Using it as a Go SDK

`Impl`s take a `context.Context` and honor whatever it carries (sandbox policy, concurrency limiter, working directory); called with a plain context they behave as thin wrappers.

```go
import "github.com/egladman/magus/std"

out, err := std.OsExec(ctx, "git", []string{"rev-parse", "HEAD"}, "", nil)
```

## See also

- [`gopherbuzz/std/README.md`](../gopherbuzz/std/README.md) - the Buzz stdlib
  this package supersets, and its upstream-parity constraint.
- [`docs/engines.md`](../docs/engines.md), [`docs/modules/`](../docs/modules) -
  the user-facing module reference.
