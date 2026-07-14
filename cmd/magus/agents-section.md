## magus

This repo is a magus workspace (magusfile.buzz at the root). magus already
holds a verified model of it: the project dependency graph, every target's
declared inputs and outputs, and what a diff affects. Use that model instead
of rediscovering it by reading files.

Query before grepping. The committed MAGUS.md lists every project, target,
and the graph's routing table.

```sh
magus query "<terms>"        # find/relate entities: kind:spell, project:web, -negation
magus explain <node>         # one node: edges, provenance, blast radius
magus path <a> <b>           # how two entities relate
magus refs <symbol>          # where a code symbol is defined and referenced
magus graph stats            # god nodes, orphans, doc coverage
```

Run work through targets, never raw language tools (`go test`, `eslint`,
`pytest` lose the cache, the sandbox, and affected tracking):

```sh
magus run <target>           # top-level targets: build, test, lint, format, generate
magus affected ci            # the final gate: full pipeline over everything a diff reaches
```

Generated files are declared. Classify changed paths before reading diffs or
committing: `magus describe file <path>...` reports each path's owning project
and role (output = generated: never hand-edit, regenerate and commit with the
source change; source = the diff worth reading; unclaimed = affects nothing).

When a magus daemon is running (`magus server start`), prefer the MCP tools
(magus_query, magus_run_target, magus_output, ...) over shelling out;
`magus describe mcp-tools` lists them all.

Durable memory: magus_memory keeps per-repo status/progress/decisions files
outside the repo, shared across sessions, branches, worktrees, and models.
Read status and decisions at session start; append dated progress and
decision entries as you work.
