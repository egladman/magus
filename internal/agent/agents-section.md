## magus

This repo is a magus workspace. magus holds a verified model of it: the project
graph, each target's inputs and outputs, and what a diff affects. Ask that model
instead of reading files to rediscover it.

Load the matching magus-* skill (in .claude/skills/, .opencode/skills/ or
.agents/skills/) before you act:

| before you                                                  | load                  |
| ----------------------------------------------------------- | --------------------- |
| git commit, add, stash or reset, or read a diff             | magus-vcs-hygiene     |
| build, test, lint, format or generate                       | magus-run             |
| grep for what exists, depends on or uses X                  | magus-query           |
| answer "how does magus X" from memory                       | magus-upstream-docs   |
| blame magus: a crash, or behavior against its docs          | magus-upstream-source |
| summarize what merged recently                              | magus-change-summary  |
| edit an installed skill, or answer recurring guard feedback | magus-workspace-rules |

Query before grepping.
{{routing-index}}

```sh
magus query "<terms>"        # find and relate entities: kind=spell, project=web, id=~regex
magus explain <node>         # one node: edges, provenance, blast radius
magus refs <symbol>          # where a code symbol is defined and used
```

Run work through targets. A raw `go test`, `eslint` or `pytest` skips the cache,
the sandbox and affected tracking:

```sh
magus run <target> <project> # build, test, lint, format, generate
magus affected ci            # the final gate over everything a diff reaches
```

The guard denies piping or redirecting magus output, and a pipe hides the exit
status. Ask magus for the shape you want:

```sh
magus <cmd> -o name          # ids only
magus <cmd> -o json          # the record; -o template='{{.Field}}' for one field
magus query output <ref>     # a run's full log
```

Before reading a diff or committing, run `magus describe file <path>...`. An
output is generated: regenerate it and commit it with its source, never hand-edit
it.

Prefer the host's magus MCP tools (`client` runs the magus module), and fall back
to the CLI when one is missing or fails. The host manages that connection: never
start a server for it.

Installed magus-* skills are stamped, and `magus agent install --force` erases an
edit. Put workspace-specific instructions in a local magus-local-development
skill beside them.
