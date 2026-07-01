# magus-affected

Run a target for VCS-diff affected projects

## Synopsis

**magus** affected \<target\> [flags]

## Description

Run a named target for every project that is affected by changes in
version control. The active VCS adapter is picked by autodetect from .git, .hg,
or .jj at the workspace root, or pinned with MAGUS_VCS_COMMAND_NAME /
vcs.command_name. MAGUS_VCS_COMMAND overrides the command entirely. When
MAGUS_VCS_ENABLED=false (or vcs.enabled: false) affected detection
short-circuits and falls back to the full project set with the source label
"vcs disabled".

A project is affected if any of its source files changed directly, or if a
project it depends on is affected (transitive closure over the dependency graph).

Use --stdin to read changed paths from a pipe instead of running a VCS diff.
This pairs with magus watch for continuous-build workflows:

magus watch | magus affected --stdin build

Forensic modes reason about the affected set instead of executing a target.
--explain shows why a project is in the set. --plan emits a provider-neutral
JSON CI shard plan for the affected set (for CI fan-out; always keys off the ci
anchor). --bisect drives VCS bisect using run history to find the commit that
introduced a regression.

## Options

**--base** *string*
: Override base ref for the VCS diff (default: MAGUS_VCS_BASE_REF or per-VCS built-in)

**--bisect** *string*
: Drive VCS bisect to find the commit that broke \<project\>

**--depth** *int*
: With --graph: cap displayed depth (0 = unlimited)

**--dry-run**
: Print what would run without executing

**--explain** *string*
: Show why \<project\> is in the affected set instead of executing

**--good** *string*
: With --bisect: known-good commit SHA (auto-detected from history when empty)

**--graph**
: Render the dependency graph for the affected scope instead of executing

**--max-parallel-budget** *int*
: With --plan: cross-shard concurrency cap; 0 = unlimited

**--max-shards** *int* (default: 8)
: With --plan: maximum CI shards (-1 = unlimited)

**--null**
: With --stdin: expect NUL-separated paths and double-NUL between batches

**--plan**
: Emit a provider-neutral JSON CI shard plan for the affected set

**--stdin**
: Read changed file paths from stdin instead of running a VCS diff

**--target** *string* (default: test)
: With --bisect: magus target to bisect

**--upstream**
: With --graph: show dependents instead of dependencies

## Targets

**ls**
: Print selected projects without executing anything

**build**
: Build selected projects

**test**
: Test selected projects

**lint**
: Lint selected projects (read-only)

**format**
: Format source files in selected projects

**clean**
: Remove build artefacts from selected projects

**generate**
: Run code generation for selected projects

**ci**
: Run the magusfile's ci target read-only (affected-set anchor)

## Examples

*Build projects changed since the default base ref*

```sh
magus affected build
```

*Use a different base ref*

```sh
magus affected build --base main
```

*Pipe from watch for continuous builds*

```sh
magus watch | magus affected --stdin build
```

*List affected projects without building*

```sh
magus affected list
```

*Show dependency graph for the affected scope*

```sh
magus affected build --graph
```

*Graph as DOT for piping to Graphviz*

```sh
magus affected build --graph -o dot | dot -Tsvg > graph.svg
```

*Emit a CI shard plan for the affected set*

```sh
magus affected --plan
```

*Shard plan limited to four shards*

```sh
magus affected --plan --max-shards 4
```

*Bisect a regression in myapp*

```sh
magus affected --bisect ./apps/myapp
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-tail**(1)](magus-tail.md), [**magus-insight**(1)](magus-insight.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-server**(1)](magus-server.md), [**magus-completion**(1)](magus-completion.md), [**magus-init**(1)](magus-init.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

