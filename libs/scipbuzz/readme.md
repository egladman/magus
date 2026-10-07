# scipbuzz

A [SCIP](https://github.com/scip-code/scip) indexer for Buzz. `scipbuzz.Index`
reads the `.buzz` files of a project and returns a `*scip.Index`; the `scip-buzz`
command writes one to disk.

```sh
scip-buzz [--output FILE] [--workspace-root DIR] [--version]
```

It indexes the working directory and writes `index.scip` there unless `--output`
says otherwise. `--version` prints `scip-buzz <semver>`.

The indexer is static. It parses with gopherbuzz's `ParseEmbedded` and never runs
the checker or any Buzz, since checking a file executes the top level of every
module it imports. Names are bound by the indexer's own scope walk. The gopherbuzz
AST keeps only a start position per node and none for declaration names,
parameters or type annotations, so every range is recovered from the token stream.
That includes interpolated expressions, whose lexer columns follow upstream's
approximate convention.

## Workspace

- `Options.ProjectRoot` (required; the command passes the working directory) is
  the directory indexed. `Document.relative_path` is relative to it.
- `Options.WorkspaceRoot` (default: the nearest directory at or above the project
  holding `magus.yaml`, else the project) is what symbol paths are relative to, so
  two projects that reference one file produce the same symbol for it.
- Discovery skips dot-directories, `testdata`, `node_modules`, `vendor`, and any
  subdirectory with its own `magusfile.buzz`, which is a project indexed on its own.
- An import resolves the way magus's gopherbuzz session resolves it: `<path>.buzz`
  beside the importing file, then the search templates magus gives a magusfile
  (`?.buzz`, `?/main.buzz`, `?/src/main.buzz`, `?/src/?.buzz`,
  `magusfiles/?.buzz`) at the project root and then at the workspace root. Like
  gopherbuzz, `.buzz` is always appended. gopherbuzz also walks the directories of
  the files that imported the importer, a chain a static reader does not have.
- The index names no absolute path, so one tree gives the same bytes from any
  checkout: `Metadata.project_root` and `ToolInfo.arguments` are left empty.

A file that does not parse is left out and reported through `Options.Warn`, as is
any construct whose position cannot be recovered exactly. No range is guessed.

## Symbols

| Entity                                        | Symbol                                   |
| --------------------------------------------- | ---------------------------------------- |
| top-level `fun` or `extern fun`               | `` scip-buzz buzz . . `P`/name(). ``     |
| top-level `final` or `var`                    | `` scip-buzz buzz . . `P`/name. ``       |
| `object`, `protocol`, `enum`                  | `` scip-buzz buzz . . `P`/Name# ``       |
| member of a module no workspace file provides | `scip-buzz buzz host . module/member().` |
| locals, parameters, type parameters, imports  | `local N`                                |

`P` is the file's path from the workspace root. The package name and version are
empty (`.`), so a symbol is the same string in every checkout. An `extern fun` is a
forward definition, not a definition.

Host modules (`fs`, `std`, `magus`, `magus/spell/go`), `project/...` handles,
remote spells and paths that do not resolve all land in the `buzz host .`
package. Their members appear in `external_symbols` with a display name and no
documentation. The source does not say whether a host member is a function or a
type, so the spelling decides, the same way in every index: a capitalized member
is a type (`#`) and anything else a function (`().`).

## Phase 1 limits

- Members of values are not indexed: fields, methods and enum cases have no symbols,
  so `x.field`, `this.m()` and `.case` produce no occurrence. Only `ns\member` and
  `ns.member` through an import are resolved.
- Parameters of top-level functions are `local` symbols, so a labeled argument
  `f(a, b: 1)` is not an occurrence of the parameter `b`.
- A bare identifier that is also a label gets no occurrence: an unlabeled argument
  after the first (`f(a, b)` is `f(a, b: b)`) and a punned field (`Rect{ w }`,
  `.{ w }`). A rename rewriting it would rename the label too, so a rename of `b`
  or `w` leaves these uses behind.
- A flat import (no alias, or `as _`) of a workspace file binds its exported names
  unqualified, its basename, and its declared `namespace a\b` path. Of a host
  module it binds only the basename: the member names are unknown without the
  module's declarations. A namespace reached relative to the importer's own
  (`here\x` from `namespace a` for `namespace a\here`) is not bound; gopherbuzz's
  checker rejects it too.
- A module the host registers natively resolves before any file in gopherbuzz. The
  indexer cannot see that registry, so a workspace file named like one (`std.buzz`
  beside the importer) is taken for the module.
- Not recorded as type references: an enum's backing type, fields of an anonymous
  `obj{...}` type, type arguments anywhere but an annotation or a call
  (`Foo::<Rect>{}`), and qualifiers deeper than one level (`a\b\Type`).
- `test "..." {}` blocks are walked for references but get no symbol.
- Enclosing ranges start at the declaration's `export` or keyword, not at its doc
  comment.
- `Test` and `Generated` roles are not set.

## Tests

The goldens under `testdata/snapshots/gen` are rendered by the SCIP bindings' own
snapshot formatter from the inputs under `testdata/snapshots/input`, one directory
per feature. `magus run snapshots-generate:rw libs/scipbuzz` rewrites them. The
external symbols of a case are written as `external_symbols.buzz`, a file of
comments, so every golden stays Buzz.

Beside the goldens, the suite checks that the bytes under every occurrence spell
the symbol's name (the check magus's rename runs before it edits a file), that the
corpus is clean under the rules `scip lint` applies, and that indexing twice, or
from two checkout paths, gives identical bytes.
