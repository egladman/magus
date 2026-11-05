---
title: "ADR 0024: magus queue runs a plugin binary"
order: 24
description: >-
  Whether the merge queue stays compiled into the magus binary. This record proposes moving it into a magus-queue binary that `magus queue` runs, the way the Docker CLI runs docker-buildx. magus looks for the plugin only beside its own resolved binary or in one fixed libexec directory, never on PATH, and only for names its command tree declares. The plugin answers a versioned metadata document and must carry magus's exact version. It links the same module's packages directly and never talks to magus over the broker. Nothing is built.
tags: [adr, decision, queue, merge-queue, plugin, cli, release, security]
status: proposed
date: 2026-11-05
---

# ADR 0024: magus queue runs a plugin binary

## Context

`magus queue` was added on 2026-09-23 in #264 and folded into `internal/queue` in #371.
[Merge queue](../concepts/merge-queue.md) opens by saying it is "compiled into magus the way
`magus buzz` is: one binary holds what it needs." It has seven subcommands (`describe`,
`ls`, `plan`, `validate`, `gate`, `apply`, `reviews`), and every one writes
`mergequeue.event/v1` lines on stdout.

What it is, measured on this tree on 2026-11-05 at `aa1597183`:

- Four packages linked into `magus`: `internal/queue`, `internal/queue/types`,
  `internal/queue/client` and `internal/queue/provider`. They hold 11,076 lines of non-test
  Go and 9,053 lines of tests. `cmd/magus/queue.go` adds 946 lines and its test 1,039.
- 42 of the 288 commits on this branch since 2026-09-23 touched `internal/queue` or
  `cmd/magus/queue.go`. 68 did when the concept page, `hack/ci/merge-queue.buzz`,
  `.github/workflows/queue.yaml` and the command tree are counted.
- It drives git alone. jj, Mercurial and Sapling refuse by name. The one built-in provider
  is GitHub. Setting it up takes a private GitHub App, a `magus-queue` environment holding
  the app's private key as a secret, and a ruleset that requires the queue's status from
  that app ([Setting it up on GitHub](../concepts/merge-queue.md#setting-it-up-on-github)).
  This repository runs it through four workflows: `queue.yaml`, `queue-apply.yaml`,
  `queue-advice.yaml` and `queue-dashboard.yaml`.
- `go list -deps ./internal/queue/...` reaches 736 packages and `go list -deps ./cmd/magus`
  reaches 819. Apart from the generated `queuetest` mocks and testify, every package the
  queue reaches is one `magus` already links. A plugin binary would therefore be close to
  the size of `magus` (72,115,538 bytes for this dev build). Moving it out does not make
  either binary small, and this record does not argue that it does.

The queue reaches past its own packages in four places, which are what make a split more
than moving a directory:

- `magus ls jobs` (`cmd/magus/job.go`) and the `magus` std module (`std/magus.go`) call
  `queue.JoinInflight`. It reads the newest `mergequeue.snapshot/v1` document that
  `magus queue ls` writes, one rewritten JSON file per base (`changes-<base>.json`).
- `internal/config/config.go` declares `queue.carry_approvals` with
  `queuetypes.CarryPolicy`.
- The generated Buzz bindings (`internal/interp/bindings/gen`, `gen/decode`,
  `gen/providerops`) import `internal/queue/types` for the provider calls
  ([ADR 0011](0011-typed-provider-calls.md#review-workspace-and-the-merge-queue)).
- `cmd/magus-utils` generates those bindings from `internal/queue/types`.

`internal/queue` imports no broker package. The queue process claims no build seat.
The hooks it runs (`--gate 'magus run ci'`, `--regenerate 'magus run generate:rw'`) are
`magus` processes, and they claim seats like any run. `magus queue` never runs through the
daemon.

Three readers weigh this differently:

- **A Linux graybeard** reads `magus -h` and finds 36 commands, one of them a merge queue
  that needs a GitHub App. The word is also taken: the [broker](../glossary/broker.md) holds
  a FIFO of [queued](../glossary/queued.md) targets waiting for a slot, and
  `magus status --watch` shows them. The [scope test](../scope.md#the-test) asks whether a
  verb answers from what magus already knows. The queue's affected sets pass it. Its
  providers, approvals, rulesets and app credentials each taught magus something new
  about the world.
- **A seasoned developer making a first contribution** to the build engine finds one commit
  in seven since 2026-09-23 in a subsystem the build engine never calls, and
  `cmd/magus/queue.go` beside the engine's own commands. The split does not change what
  `magus affected ci` reaches, since both binaries stay in one module.
- **A first-time adopter** expects `run`, `test` and `affected`. Today they get the queue
  whether or not they ever create the app. Nothing in `magus -h` says it is the
  only command that needs outside setup before it does anything.

## Decision

The queue leaves the `magus` binary. A `magus-queue` binary, built from `cmd/magus-queue`
in the same Go module, holds it. `magus queue <args>` finds that binary and runs it with
`<args>`. The command tree keeps declaring `queue`, so help, completions and man pages
still come from one source. Every row below is _proposed_.

### Lookup: beside the binary or one libexec directory, never PATH

magus resolves its own path with `os.Executable` and `filepath.EvalSymlinks`, as
`internal/selfupdate` already does on macOS, and looks in exactly two places, in order:

1. `<dir of magus>/magus-<name>`
2. `<dir of magus>/../libexec/magus/magus-<name>`

On Windows the name carries `.exe`. There is no third place. magus searches no `PATH`,
reads no `MAGUS_PLUGIN_PATH` or other variable, and has no per-user plugin directory.

It looks up only names its command tree declares as plugin commands, `queue` alone today.
It never scans a directory for `magus-*` files. This tree has nine other `cmd/magus-*` main
packages (`magus-utils`, `magus-docs`, `magus-manpage` and six more), and
`go install ./cmd/...` puts all of them in one `GOBIN` beside `magus`. A scan would offer
`magus utils` as a command.

The file found must be a regular file, not a symlink, and neither it nor its directory may
be writable by group or others. Any other answer is refused, naming the path and the
check it failed. A missing plugin is refused with a diagnostic that names both paths
searched and the release archive that carries `magus-queue`. magus never falls back to an
older built-in queue
([No silent defaults](../design/judgment/no-silent-defaults.md)).

Why this prevents running arbitrary code: `PATH` belongs to the environment, and a checkout
can shape the environment (mise `env`, direnv, `node_modules/.bin`, a CI step that
prepends `./bin`). `queue apply` holds the write credential and promises to run no change's
code ([Trust model](../concepts/merge-queue.md#trust-model)). With a `PATH` lookup, a pull
request that adds `bin/magus-queue` runs with that credential on any runner whose `PATH`
reaches the checkout. The directory beside the resolved `magus` is writable by whoever
installed `magus`. Trusting it is trusting `magus` itself, and running magus is already
that consent ([ADR 0014](0014-running-magus-is-the-consent.md)). A per-user directory would
be writable by every process the user runs, which is the same hole with a different path.
Git's `GIT_EXEC_PATH` (`libexec/git-core`) is the prior art for the libexec directory.
Git's fallback to `PATH` is the part not taken.

### Handshake: a versioned metadata document, exact version

Before it runs a plugin, magus runs `magus-queue magus-plugin-metadata` with a fixed
timeout and reads one JSON document from stdout:

```json
{
  "apiVersion": "magus.plugin/v1alpha1",
  "kind": "PluginMetadata",
  "name": "queue",
  "version": "v0.6.0",
  "short": "Merge approved changes through a speculative, partitioned merge queue",
  "schemas": [
    "mergequeue.event/v1alpha1",
    "mergequeue.changes/v1alpha1",
    "mergequeue.plan/v1alpha1",
    "mergequeue.verdict/v1alpha1",
    "mergequeue.capabilities/v1alpha1",
    "mergequeue.reviews/v1alpha1",
    "mergequeue.snapshot/v1alpha1"
  ]
}
```

The schema names follow the Kubernetes-style versions decided on 2026-11-05. Today's
constants are `/v1` (`internal/queue/types/schema.go`, `internal/queue/events.go`,
`internal/queue/snapshot.go`). Renaming them belongs to that sweep, not to this record.

magus refuses, naming both sides, when:

- `apiVersion` is one it does not read: it names the one it got and the ones it reads.
- `name` is not the name it looked up.
- `version` is not exactly magus's own `version`: it names both versions and both paths.
- `schemas` lacks a schema magus itself reads: today `mergequeue.snapshot/v1alpha1`, for
  `magus ls jobs`.

The version must be equal, not compatible. `magus-queue` links internal packages of the
same module (`internal/config`, `internal/sandbox`, `internal/interp`, `internal/job`,
`internal/risk`, `internal/cache`), and none of them promises behavior across versions.
magus also prints the plugin's help from its own command tree. A range would need a
compatibility promise that neither binary keeps before 1.0
([Break now, hold still later](../design/principles/break-now-hold-still-later.md)).
`apiVersion` versions the document's shape, so a skewed pair can still read each other's
metadata well enough to say what is wrong.

After the handshake, magus replaces its own process with the plugin (`execve` on Unix). On
Windows it starts the plugin, waits, and exits with its status. The plugin gets the
original argv after `magus`, global flags included, and the environment unchanged. magus
adds no variable. The plugin parses that argv with the same command tree magus uses, so
`magus -C ../other queue ls` and `magus queue ls -C ../other` mean what they mean today.

### Communication: the plugin links the module, and the broker is not a channel

`cmd/magus-queue` sits in the magus module, so Go's `internal/` rule lets it import what
`internal/queue` imports today, unchanged:

- the SDK root (`magus.Open` in `internal/queue/client`) for affected sets and the
  workspace's declared outputs, with the workspace loaded once;
- `internal/sandbox` for the box `validate` and `gate` run hooks in;
- gopherbuzz and the generated bindings for the provider scripts.

Between `magus` and `magus-queue` there is argv, an exit status, the metadata document and
the files both read. There is no RPC.

The broker is not the channel, for three reasons found in the tree. It holds capacity and
shared services, not workspace facts, so it would have to learn to serve affected sets it
does not compute. `broker: off` in `magus.yaml` runs without one, and the queue must work
there. `magus queue` never runs through the daemon today. The plugin stays a non-client of
the broker. Its hooks, being `magus` runs, claim seats exactly as they do now.

### What moves and what stays

| Item                                                                  | Where it goes                                                                                                                                                 | State    |
| --------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------- |
| `internal/queue` (root), `internal/queue/client`, `.../provider`      | linked only by `cmd/magus-queue`; a conformance check fails a `cmd/magus` import of them                                                                      | proposed |
| `cmd/magus/queue.go` and its test                                     | `cmd/magus-queue`                                                                                                                                             | proposed |
| the `queue` entry in `internal/commands/registry.go`                  | stays, marked as a plugin command; both binaries read it, magus's run function is the dispatch                                                                | proposed |
| help, completions, man page (`docs/reference/manpage/magus-queue.md`) | stay generated from that entry, so they work with no plugin installed                                                                                         | proposed |
| `magus -h`                                                            | lists `queue` in its declared group with its `Short`, suffixed `(not installed)` when neither path holds a file; found by `stat`, never by running the plugin | proposed |
| `internal/queue/types`                                                | stays shared: the documents, codes and provider records both binaries and the bindings read                                                                   | proposed |
| `NewestSnapshot`, `JoinInflight` and the snapshot decoder             | leave the root `queue` package for a package `magus` may import; the snapshot becomes a document two binaries read                                            | proposed |
| `queue.carry_approvals`                                               | stays in `internal/config/config.go`, the one source of config keys                                                                                           | proposed |
| `internal/interp/bindings/gen` provider ops                           | stay where `cmd/magus-utils` generates them                                                                                                                   | proposed |
| `go-build` target                                                     | builds `./magus-queue` beside `./magus`                                                                                                                       | proposed |

### Release and packaging

| Item                | Decision                                                                                                                                                                                               | State    |
| ------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | -------- |
| release archive     | `release-build` builds `magus-queue` for every platform `magus` ships and puts it beside `magus` in the same `magus_<v>_<os>_<arch>_<variant>.tar.gz`, not in an archive of its own as `proofread` has | proposed |
| signing             | the archive's existing signature covers both binaries                                                                                                                                                  | proposed |
| `magus self update` | installs both from the one verified archive, `magus-queue` first. A failure between the two leaves a skewed pair, which the next `magus queue` refuses naming both versions                            | proposed |
| `setup-magus`       | installs both from a release, or builds both from source                                                                                                                                               | proposed |
| container image     | carries both                                                                                                                                                                                           | proposed |
| skew                | exact version equality, refused loudly; no compatibility window                                                                                                                                        | proposed |

### The breaks

Recorded per [A deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md).
No comparison gate in this tree compares the command set (no `*.deviations.jsonl` is
committed today), so the change that builds this carries every consumer's update in the
same diff and lands green. Each break gets a `### Removed` or `### Changed` entry in its
`changes/` fragment.

1. `magus queue` fails on an install holding only `magus`. That covers
   `go install github.com/egladman/magus/cmd/magus@<v>`, a copied binary, and any package
   channel that places one file. The failure names both paths and the archive.
2. `go install .../cmd/magus` alone no longer gives a queue. The setup guides gain
   `go install .../cmd/magus-queue@<v>` into the same `GOBIN`.
3. The concept page's "compiled into magus the way `magus buzz` is" becomes false. The page
   changes in the same commit, along with
   [The packages](../concepts/merge-queue.md#the-packages).
4. `mergequeue.snapshot` becomes a contract between two binaries. Its format does not
   change here.
5. No flag, subcommand, event or document changes. The schema renames belong to the
   Kubernetes-style versions sweep.

### Principles checked

- [What we do not build is the product](../design/principles/what-we-do-not-build.md): relied
  on. The core stops carrying a GitHub-App-shaped feature that most workspaces never
  configure.
- [Capability is not a reason](../design/principles/capability-is-not-a-reason.md): strained.
  A plugin mechanism is a new capability. The failure it removes is the queue's place in
  every install and every reading of `magus -h`. The mechanism is closed to one declared
  name so it cannot grow into a general extension point by default.
- [Unix survives at the verb, not the binary](../design/principles/unix-survives-at-the-verb.md):
  departed from. That page argues against making the binary smaller than the problem. The
  queue is not part of the build tool's problem, and the split follows the scope test the
  page names. The main package still only wires.
- [One source, every door](../design/principles/one-source-every-door.md) and
  [Flags, config and help from one source](../design/principles/flags-config-help-one-source.md):
  relied on. The queue's flags, help and completions stay in `internal/commands`. Neither
  binary has a second parser.
- [Delete before you add](../design/principles/delete-before-you-add.md): strained. The split
  adds a dispatch, a handshake, a second release artifact and a skew diagnostic. Deleting
  the queue reaches the goal too, and is rejected under Alternatives only because this
  repository merges through it.
- [Sit next to the tools](../design/principles/sit-next-to-the-tools.md): relied on. The
  plugin is a plain file beside `magus`, and running `magus-queue ls` directly does what
  `magus queue ls` does.
- [No silent defaults](../design/judgment/no-silent-defaults.md): relied on. Missing, skewed
  and unsafe plugins are refused by name. Nothing falls back.
- [Break now, hold still later](../design/principles/break-now-hold-still-later.md): relied
  on for exact version equality and for breaking the single-binary install with no
  migration path.
- [Everything is a file](../design/principles/everything-is-a-file.md): strained by an
  existing format, not by this decision. The snapshot that becomes a cross-binary contract
  is one rewritten JSON document per base, not JSON Lines.

## Alternatives

### Keep the built-in subcommand

It loses because every install and every `magus -h` carries a merge queue that only works
after a GitHub App, an environment secret and a ruleset are set up. A contributor to the
engine also meets 20,000 lines of queue code and tests in the same binary. The evidence is
the measurements under Context: 4 linked packages, 42 of 288 commits, git-only, one
provider. It reopens if a second package channel the project supports cannot place two
files, or if the dispatch and skew diagnostics cost more maintenance than the queue's
presence in the core does.

### A separate CLI with no `magus queue` dispatch

It loses because the queue would need a second command root for its global flags (`-C`,
`--root`, `-o`, verbosity), its help and its man page, which breaks one source for flags.
Every workflow and document that says `magus queue` would change for no gain. The dispatch
this record adds is one lookup and one `exec`. Evidence: the `queue` entry in
`internal/commands/registry.go` already reads magus's global flags, and the four workflows
call `magus queue`. It reopens if the queue moves to its own module and release with its
own version, where a dispatch tied to magus's exact version would be wrong.

### A spell or a Buzz script

It loses on the rule the concept page states: Go holds what the invariants are proven over
and what needs the machine (admission, partitioning, candidate order, merge checks,
version control, processes, files, concurrency). Buzz holds the provider, as a pure
function of a system's answers. A spell is a composed procedure behind a thin, sandboxed
interface. `apply` must hold a write credential and push, which a spell sandbox is built
to deny. Evidence: 11,076 lines of Go with testify suites, and the providers that are
already Buzz (`internal/queue/provider`). It reopens if that Go/Buzz rule is reversed by
its own record.

### Docker's discovery: any `magus-*` in a list of directories, a user directory included

Docker searches `~/.docker/cli-plugins` and system directories and lists whatever plugin
it finds. It loses here on two counts. A user directory is writable by any process the user
runs, so it reopens the hole the `PATH` ban closes. A scan would also list the nine
`cmd/magus-*` development binaries that `go install ./cmd/...` places beside `magus`. It
reopens when a second plugin exists whose name cannot be declared in the command tree
ahead of time.

### A separate Go module for the queue

`libs/proofread` ships from its own module. It loses because `internal/queue` imports
fifteen `internal/` packages of the magus module (`internal/sandbox`, `internal/interp`,
`internal/config`, `internal/job` and eleven more), which a separate module cannot import.
Serving them would put them in the public SDK, which this project is shrinking.
It reopens when the queue's needs are met by exported SDK packages alone.

### One binary, with `magus-queue` as a link to it

It loses because it removes nothing from `magus`. It changes only the name a person types.

### Delete the queue

It loses because this repository merges through it, and the four queue workflows would
need a replacement. It reopens if the repository stops merging through `magus queue`.

## Consequences

- `magus` stops linking `internal/queue`, `client` and `provider`. It still links
  `internal/queue/types` through the bindings and config.
- Each archive carries two binaries of about 70 MB each uncompressed, nearly all of it
  shared packages. The compressed size has not been measured.
- A contributor's `./magus` needs `./magus-queue` beside it to run the queue. The `go-build`
  target builds both. The guard still looks only for `./magus`.
- A skewed pair fails at `magus queue` with both versions named. It is never a quiet wrong
  answer.
- `magus ls jobs` keeps working with no plugin installed, because the snapshot reader stays
  in `magus`. With no snapshot it joins nothing, as it does today.

## Open questions

- **Is the archive size acceptable?** Two ~70 MB binaries per platform, for a feature most
  installs never use. A separate `magus-queue_<v>_<os>_<arch>.tar.gz` archive halves
  the default download, but `self update` and `setup-magus` then fetch and verify two
  artifacts and can skew between them.
- **Does mise place both binaries?** The mise guide installs through ubi and aqua, which
  pick one executable from an archive. Whether they can place `magus-queue` beside `magus`
  is unverified.
- **Should magus stop linking `internal/queue/types`?** That needs the queue's provider ops
  generated into a package only `cmd/magus-queue` imports, with `queue.carry_approvals`
  read by the plugin alone. It is more change for little gain in binary size.
- **Where does the snapshot reader live, and under what name?** It needs a package `magus`
  may import that the no-import check on `internal/queue` does not cover.
- **What is the handshake timeout?** One fixed value, refused by name when exceeded. 5 s is
  the starting proposal.
- **Does `libexec/magus` earn its place?** No install this project documents uses it today.
  It is there for distribution packages that keep `bin/` to one entry. Dropping it leaves
  one lookup path.
