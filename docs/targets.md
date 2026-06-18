# Anatomy of a magus Target

A **Target** is the addressable unit of work in magus: _what project_ (Path) and _what operation_ (Name). Everything else (shards, spell filters, charms) is configuration layered on top, not part of the Target's identity.

A target is _what_ you run (`magus run lint`); a spell is _how_ a tool does it. See [Spells vs Targets](spells.md#spells-vs-targets).

## The struct

```go
type Target struct {
    Path   string   // project path relative to workspace root
    Name   string   // the target name; the operation to run
    Charms []string // execution charms (modifiers); see docs/charms.md
    Files  []string // changed files; populated only by ExpandAffected
}
```

`Path` and `Name` are the two durable identities. `Charms` modify _how_ the target runs (see [charms.md](charms.md)). `Files` is metadata populated automatically by the VCS-affected engine; nil for explicitly constructed targets.

## CLI grammar

```
[spell::]op-or-name[:charm,...]  [project ...]
   │       │           │              │
   │       │           │              └── projects, positional (omit = cwd/all)
   │       │           └───────────────── charms, comma-separated modifiers
   │       └───────────────────────────── a target name, or (with spell::) a spell op
   └───────────────────────────────────── spell, CLI-only op-direct qualifier
```

The project is a **positional** argument, not embedded in the target token. `:` introduces [charms](charms.md); `spell::op` invokes one spell's op directly (see [spell-qualified targets](#cli-extension-spell-qualified-targets)).

Examples:

| Command                        | Project(s) | Name           | Charms           | Spell |
| ------------------------------ | ---------- | -------------- | ---------------- | ----- |
| `magus run build`              | cwd / all  | `build`        | -                | -     |
| `magus run test api`           | `api`      | `test`         | -                | -     |
| `magus run format:rw api`      | `api`      | `format`       | `rw`             | -     |
| `magus run lint:rw,debug /`    | all        | `lint`         | `rw`, `debug`    | -     |
| `magus run go::go-test`        | all        | `go-test` (op) | -                | `go`  |

Invalid forms:

| String            | Reason                                                   |
| ----------------- | -------------------------------------------------------- |
| `""`              | empty target rejected                                    |
| `lint:`           | charm must not be empty                                  |
| `web/studio:test` | `/` not allowed in a target name (project is positional) |

The canonical serialized form of a resolved Target (what `Target.String()` emits and what `describe`/logs show) is `path:name` (e.g. `web/studio:test`). That is the output form; the grammar above is how you type it.

## Path resolution on the CLI

`Path` is stored **relative to the workspace root**, but the CLI accepts project names from anywhere in the tree. Arguments to `magus run`, `magus list`, and `magus clean` resolve as follows:

| Input                        | Resolved against           | Example (cwd = `web/studio`) |
| ---------------------------- | -------------------------- | ---------------------------- |
| bare (`api`, `web/studio`)   | workspace root             | `api` → `api`                |
| dot-relative (`./x`, `../x`) | current working directory  | `../api` → `web/api`         |
| `.`                          | the project containing cwd | `.` → `web/studio`           |
| empty or `/`                 | all projects               | n/a                          |

So `../foo` behaves as a shell user expects: from `web/studio`, `magus run build ../foo` targets `web/foo`. Bare paths stay workspace-relative regardless of cwd. Two inputs are always rejected:

- **Absolute paths** (`/etc`, `C:\foo`): project paths must be repo-relative.
- **Paths that escape the workspace root** (`../../outside`): magus never operates outside the workspace it discovered.

Resolution is implemented by `internal/file/path.Resolve(input, anchor)`, where the anchor is the cwd expressed relative to the workspace root. The same helper backs `WithDependsOn` in a magusfile, so dependency paths and CLI arguments obey identical rules.

### Symlinks

The workspace root is canonicalised (symlinks resolved) at discovery, and the sandbox enforces access against **real, resolved paths** on Linux via the kernel landlock LSM. A symlink inside the workspace pointing at `/etc` grants no access to `/etc`.

Two consequences:

- **Symlinked directories are not discovered as projects.** Discovery does not follow symlinks; a symlinked directory is silently skipped.
- **Workspace-escaping symlinks are a hard error.** `magus doctor` fails if it finds a symlink whose resolved target lands outside the workspace root. On platforms without landlock (macOS, Windows, kernels < 5.13) such a link is the only path by which a spell subprocess could reach outside the tree, so it is treated as a `fail`, not a warning.

## The target name

A target name is one of the seven canonical operations. The type is `project.Target` (a `string` alias).

| Name        | Meaning                                          |
| ----------- | ------------------------------------------------ |
| `preflight` | pre-run checks (workspace health, missing tools) |
| `build`     | compile / produce artefacts                      |
| `test`      | run the test suite                               |
| `lint`      | static analysis, type-check                      |
| `format`    | format source files                              |
| `clean`     | remove local build artefacts                     |
| `generate`  | run code generators                              |

There is also `ci`: a composite pipeline (preflight → generate → format → lint → build → test) handled specially by `Magus.RunCI`. It is not in the `project.Targets` list and is not a valid name for `ParseTarget`.

Custom target names must use the target-name charset: letters, digits, `-`, `_` (`types.ValidateTargetName`). `:`, `@`, and `/` are reserved for the grammar above.

### Name normalization (casing & delimiters)

Target names are matched **case- and delimiter-insensitively**. magus normalizes every name to canonical kebab-case (`lo.KebabCase`, via `types.DefaultTargetNameNormalizer`) on **both** sides: when a magusfile _declares_ a target and when you _reference_ one on the CLI or in `depends_on`. A target declared as `go_build` is reachable by any spelling that normalizes to `go-build`:

```sh
magus run go-build      # kebab
magus run go_build      # snake
magus run goBuild       # camel
magus run GoBuild       # pascal
```

This is _normalize-both-sides_, not an alias table: there is exactly one registered target (`go-build`), and the same normalizer runs over your input before lookup.

- **Collisions are a hard error.** Two declarations that collapse to the same canonical name (e.g. `fooBar` and `foo_bar`, both normalizing to `foo-bar`) cause magus to refuse the magusfile, naming the offending pair.
- **Convention drift is a `doctor` warning.** Mixed conventions still resolve, but `magus doctor` warns when a workspace uses more than one naming convention, since call sites across CI YAML, scripts, and docs can drift.

**Terminology note:** Target is magus's own term, distinct from Mage's vocabulary. In Mage, `extensions:build` is a single function name. In magus, `extensions` is a project Path and `build` is the target name: two orthogonal axes. Do not substitute Action, Operation, Task, Command, or Verb for Target in code, comments, or documentation.

## CLI extension: spell-qualified targets

On the command line only, a double-colon prefix invokes one spell's op **directly**, bypassing your composed targets. The token after `::` is a spell **op** (its CLI-command name), not a lifecycle target name:

```sh
magus run typescript::eslint api    # the eslint op of the typescript spell, in api/
magus run go::go-vet                # the go-vet op of the go spell, all projects
magus run go::golangci-lint         # the golangci-lint op of the go spell
```

This is an **escape hatch** for ad-hoc runs and introspection, not the everyday surface (compose ops into [targets](spells.md#spells-vs-targets) instead). Because it is op-direct, the name after `::` is matched against the spell's op keys verbatim (no kebab/case normalization, unlike target names — see [Naming operations](spells.md#naming-operations)):

- `go::golangci-lint` runs that op.
- `go::lint` is a graceful **no-op**: the go spell has no op named `lint` (its linter op is `golangci-lint`), so nothing runs.

The prefix is **not** stored in `Target`. The CLI strips it via `parseTarget` and passes it as a `WithSpellFilter` `RunOption`. The `ci` target does not support spell-qualified syntax.

## What is not part of a Target's identity

These modify execution but are **not** durable identity. Charms parse into `Target.Charms` but propagate via context; the rest travel as `RunOption` values alongside the target list.

| Input                    | Purpose                                                           |
| ------------------------ | ----------------------------------------------------------------- |
| `:charm,...`             | shared execution modifiers (see [charms.md](charms.md))           |
| `--shard` / `--n-shards` | CI matrix sharding (distributes projects across runners)          |
| `--dry-run`              | prints what would run without executing                           |
| extra args after `--`    | forwarded to the underlying tool via `WithExtraArgs`              |

## Lifecycle: parse → expand → run

A target string goes through three stages before any tool is invoked:

```
"web/studio:test"  (or: name token + positional projects)
      │
      ▼
ParseTarget(s)          → Target{Name:"test", Charms:[...]}
      │                   magus/types/target.go
      ▼
Workspace.ExpandPath(t) → []Target (one concrete entry per matched project)
      │                   magus/select.go
      │
      │   (alternative: ExpandCwd resolves for the project under cwd)
      │   (alternative: ExpandAffected uses VCS diff to select projects,
      │                 and populates Target.Files)
      ▼
Magus.Run(ctx, targets) → executes each target, grouped by Name; charms
                          ride along on the context (WithCharms)
```

Key invariant: targets passed to `Run` should be concrete (each Path resolves to exactly one project). `ExpandPath`, `ExpandCwd`, and `ExpandAffected` enforce this.

## Glossary

| Term       | Definition                                                                                                          |
| ---------- | ------------------------------------------------------------------------------------------------------------------- |
| **Target** | An addressed unit of work: `Path + Name + Charms + Files`. The `Target` struct in `magus/types/target.go`.          |
| **Path**   | Project path relative to the workspace root. Empty or `/` means all projects.                                       |
| **Name**   | The target name: the operation to run. One of: `preflight`, `build`, `test`, `lint`, `format`, `clean`, `generate`. |
| **Charm**  | A shared execution modifier (e.g. `rw`). Carried in context; see [charms.md](charms.md).                            |
| **Files**  | Repo-relative changed paths within a project. Populated by `ExpandAffected`; nil for explicit targets.              |
| **Spell**  | A library of tool-native operations a target composes. Separate from Target; see [spells.md](spells.md).            |
| **`ci`**   | The composite CI pipeline. Not a target name; handled by `Magus.RunCI`.                                             |

## See also

- [operations.md](operations.md): the formal Operation definition and the work hierarchy (Spell → Operation → Target).
- [spells.md](spells.md): the operations a target composes, and [Spells vs Targets](spells.md#spells-vs-targets).
- [charms.md](charms.md): the execution modifiers attached after `:`.
- [engines.md](engines.md): the Buzz engine a magusfile runs on.
