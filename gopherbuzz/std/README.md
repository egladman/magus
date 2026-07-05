# gopherbuzz/std - the Buzz standard library

This package implements the **standard library of the Buzz language** for the
gopherbuzz VM: the modules a Buzz program reaches with a bare `import` -
`std`, `math`, `fs`, `os`, `crypto`, `gc`, `debug`, `io`, `serialize`,
`buffer`, and `ffi`.

```go
sess := buzz.NewSession(ctx)
buzzstd.Register(sess) // makes every module above importable
```

## The constraint: match upstream Buzz

These modules are **the language's** standard library, not gopherbuzz's own
invention. gopherbuzz is a pure-Go VM for [Buzz](https://buzz-lang.dev/); it
targets 0.6.0-dev, tracking upstream `buzz-language/buzz` `main` at the commit
pinned in [`../version.go`](../version.go) (`UpstreamRef`), and this package must
track the upstream stdlib's **names, signatures, and observable semantics** so a
Buzz program runs the same here as on the reference implementation. The latest
published [reference](https://buzz-lang.dev/0.5.0/reference/) is 0.5.0; 0.6.0 is
unreleased.

The project-wide rule (see the top-level [README](../README.md)) is **"match
capabilities, diverge only where a concrete reason forces it."** Concretely, for
this package:

- **Do not add upstream-shaped modules or methods here that upstream Buzz does
  not have.** A capability magus wants - running a subprocess, querying git,
  hashing a file - is a _host_ concern and belongs in
  [`magus/std`](../../std/README.md), layered on top (see below). Keeping the
  upstream-faithful surface upstream-shaped is what lets a standalone `.buzz`
  program (and `cmd/buzz`) stay portable. The one sanctioned exception is
  gopherbuzz's own test surface (`assertcore`/`assert`/`suite`/`testing`), which
  has no upstream counterpart; those register through `std.Modules` carrying the
  `buzz.LabelGopherbuzz` label, so a caller can filter to the upstream-only
  surface, and conformance fixtures never import them.
- **Match signatures and return shapes** to the upstream reference. When in
  doubt, the reference at buzz-lang.dev is the source of truth.
- **Document any deliberate divergence.** Where gopherbuzz intentionally differs
  (e.g. `test` is a soft keyword, not hard-reserved), the divergence is called
  out in the top-level README's compatibility notes. A new divergence needs the
  same treatment - a comment explaining _why_, not a silent behavior change.

## Relationship to `magus/std`

magus uses this package as the base layer and then exposes a **superset**:

```text
import "os"  in a magusfile
  ├── os.sleep / os.time / os.env / os.execute / …   ← gopherbuzz/std (this package)
  └── os.exec / os.which / os.retry / os.with_env / …← magus/std (host methods)
```

magus registers this package first, then layers its host methods onto the same
bare module names and adds modules Buzz has no concept of (`vcs`, `archive`,
`http`, `env`, `time`, `charm`, …). The union is what a magusfile sees. The
cross-reference of which magus method has a native Buzz equivalent lives in
[`host/overlap.go`](../../host/overlap.go); the superset itself is
described in [`std/README.md`](../../std/README.md).

The split is deliberate: edit **this** package only to track upstream Buzz; put
anything magus-specific in `magus/std`.
