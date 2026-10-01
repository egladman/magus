# hack/

Buzz scripts for working on this repository, one directory per caller. Run a script
with `magus buzz`, passing its flags after `--`:

```sh
magus buzz hack/dev/<script>.buzz -- --help
```

| directory | holds | who calls it |
| --- | --- | --- |
| `dev/` | scripts for working in a checkout | a person or an agent |
| `remote/` | prefixes that run a command somewhere else | a person or an agent |
| `bench/` | measurements of magus itself | a person comparing two builds |
| `ci/` | workflow steps, and the scripts that act on pull requests and the repository | CI, the merge queue, a maintainer |
| `magusfile/` | modules with no `main` | magusfiles and CI, through targets |
| `lint/` | this repository's file linter: `run.buzz` and one file per rule | the root `lint-rules` target |
| `policy/` | this repository's guard rules | `magus guard`, through the root magusfile |
| `git-hooks/` | git hooks | git, once the root `git-hooks-install` target installs them |
| `docs/` | the committed screenshots' capture | a person refreshing them |
| `testdata/`, `testdata-upstream/` | the fixtures the scripts' tests read | the tests |

A script's name starts with what running it does. `ls`, `show`, `count`, `diff`,
`summarize` and `example` only read. `render` only prints. `time` runs what it names and
reports how long, writing nothing in the tree. `rename`, `split`, `mark`, `prune` and
`merge` print a plan and write only with `--apply`. `bootstrap` prepares the checkout it
runs in, writing there and to its job's row only. `on-<where>` runs the command after it
somewhere else. The first line a
script prints says what it did, and the first line of `--help` says what it costs.
`hack/lint/hack-scripts-start-with-a-verb.buzz` holds every script to that and this file
to listing each one; `hack/lint/magusfile-modules-have-no-main.buzz` keeps modules and
scripts in their directories.

Each script's work is an exported, typed function that `main` only calls, so the
scripts are also reference to copy: read one before reaching for python, sed or jq.

## dev/

| script | effect | cost |
| --- | --- | --- |
| `magus buzz hack/dev/rename-symbol.buzz -- --symbol <name> --to <new> [--apply]` | renames a code symbol at every occurrence the index verifies; dry run by default | needs the code index |
| `magus buzz hack/dev/split-into-branches.buzz -- [--base <rev>] [--prefix <branch>] [--apply]` | splits the working change into stacked branches, one per owning project; dry run by default, never pushes | grows with tree size |
| `magus buzz hack/dev/example-typed-results.buzz -- [--file <path>] [--query <terms>] [--symbol <name>]` | read-only: magus's results as typed Buzz values, where a shell pipes `-o json` into jq | needs the code index |
| `magus buzz hack/dev/ls-uncommitted.buzz` | read-only: uncommitted files by owning project and declared role | grows with tree size |
| `magus buzz hack/dev/ls-worktrees.buzz -- [--dirty]` | read-only: every worktree with its uncommitted files, unpushed commits and job | grows with the number of worktrees |
| `magus buzz hack/dev/ls-test-failures.buzz -- [--ref <output-ref>]` | read-only: a failed Go test run as `file:line: message` lines | none |
| `magus buzz hack/dev/show-interrupted-session.buzz -- [--host <host>] [--session <id>]` | read-only: a session that hit a usage limit or crashed, its checkout's state, and the commands that carry its work here | none |
| `magus buzz hack/dev/show-memory-kills.buzz -- [--since <duration\|timestamp>] [--until <timestamp>] [--name <substring>] [--all]` | read-only: the processes the OS killed or suspended under memory pressure in a window, and the swap state around them | none; reads the OS log |
| `magus buzz hack/dev/count-symbols.buzz -- [--project <path>] [--kind <kind>]` | read-only: a project's code symbols by kind and by the first word of their names | needs the code index |
| `magus buzz hack/dev/count-refusals.buzz` | read-only: this checkout's recurring guard refusals by rule | none |
| `magus buzz hack/dev/show-feedback.buzz -- [--session <id>] [--since <duration\|RFC3339>] [--until <RFC3339>] [--page <n>] [-o json]` | read-only: one session's guard feedback, ten labelled rows a page: refused, advised, unguarded command shapes, served nexts not taken; each page ends with the command for the next | every checkout's trail that changed in the window |
| `magus buzz hack/dev/mark-feedback.buzz -- --verdict should-deny\|should-advise\|wrong-deny\|fine [--note <text>] [--session <id>] [--since ...] [--until ...] [--apply] -- <label\|id>...` | records a verdict on feedback rows under their stable ids in the per-repository store; dry run by default | as show-feedback |
| `magus buzz hack/dev/count-feedback.buzz -- [--since <duration>] [-o json]` | read-only: every mark across sessions folded into a ranked guard backlog | none |
| `magus buzz hack/dev/show-session-figure.buzz -- [--session <id>] [--since ...] [--all] [--out <file.svg>] [--theme light\|dark] [--console <base>]` | a session's job hierarchy as an SVG and a console link that explores it; writes only the `--out` file | every checkout's trail that changed in the window, and the job store |
| `magus buzz hack/dev/bootstrap-worktree.buzz -- --job <id>` | builds ./magus, builds the graph and takes the job's lease; refuses unless the row names this worktree and its checkpoint | a Go build and a graph build, about two minutes cold |
| `magus buzz hack/dev/render-brief.buzz -- --job <id> [--extra <file>]` | read-only: a worker's complete brief from its job row | two job-store reads |
| `magus buzz hack/dev/show-guard-health.buzz` | read-only in the tree: replays a canonical set of tool calls through the hooks `.claude/settings.json` wires and fails when the guard lets one through | one hook process per call |
| `magus buzz hack/dev/summarize-transcript.buzz -- [--match <pattern>] -- <file>...` | read-only: a session transcript's calls per tool, refusals by rule, retries, followed suggestions and tokens | grows with transcript size |
| `magus buzz hack/dev/diff-dirs.buzz -- --a <dir> --b <dir> [--unified]` | read-only: two trees compared file by file, declared outputs left out | grows with tree size |
| `magus buzz hack/dev/show-review-context.buzz -- [--rev <base>...<head> \| --patch <file\|-> \| --from <diff.json\|->] [--baseline <graph.json>] [--budget <n>] [--lens architecture\|code\|all] [-o json]` | read-only: a change's per-symbol review context (callers, callees, tests, the path to the API, cited diagnostics) and what it does to projects, dependencies and vocabulary | needs the code index; about six graph reads per carded symbol |
| `magus buzz hack/dev/host-schemas.buzz -- verify \| fetch` | named before the verb rule: `verify` reports the vendored agent-host schemas that moved upstream, `fetch` rewrites them | network |

A pull request's patch reads the same way: `gh pr diff <n> | magus buzz hack/dev/show-review-context.buzz -- --patch -`.

## remote/

The command after `--` runs there. Its stdout, stderr and exit code are its own. These two
read their options by hand rather than through the `flags` module: the options come
first and the first word that is not one starts the command, verbatim, which ADRs 0003
and 0004 pin and `flags` cannot express.

| script | runs the command | cost |
| --- | --- | --- |
| `magus buzz hack/remote/on-actions.buzz -- <command>` | on a GitHub Actions runner: proves something on Linux without a pull request | network; pushes a scratch branch and spends CI minutes |
| `magus buzz hack/remote/on-linux.buzz -- <command>` | in a local Linux container, through Podman | a local container, plus an image pull the first time |

## bench/

| script | measures | cost |
| --- | --- | --- |
| `magus buzz hack/bench/time-startup.buzz -- [--binary <path>] [--cases <name,...>] [--count <n>] [--runs <n>]` | cold starts of a release magus against a fresh workspace, as benchstat input | one Go build, then cases x count x runs processes |

## ci/

| script | does | cost |
| --- | --- | --- |
| `hack/ci/merge-queue.buzz` | the merge queue's workflow steps, one subcommand each | network |
| `magus buzz hack/ci/pull-requests.buzz -- status \| apply \| dashboard` | named before the verb rule: each open pull request's state and the action it needs; `apply` queues and reruns; the queue workflows render `dashboard` | network |
| `magus buzz hack/ci/labels.buzz -- [--repo <owner/name>]` | named before the verb rule: prints the `gh label` commands for this repository's label taxonomy | none |

`ci/completion-checks/` holds the shell scripts the root `completion-test` target runs in
each shell's official image.

## magusfile/

Targets import these; each runs through its target rather than by hand.

| module | holds |
| --- | --- |
| `hack/magusfile/advisories.buzz` | advisory scanning that fails only on findings you can act on today |
| `hack/magusfile/branch-stack.buzz` | the typed BranchStack record split-into-branches emits and merge-job-branches reads, with its strict reader, writer and layer ordering |
| `hack/magusfile/changelog.buzz` | the changelog fragments' one grammar and renderer |
| `hack/magusfile/commits.buzz` | the conventional-commit rule the pull request title check and the commit hook share |
| `hack/magusfile/drift.buzz` | drift measured by content, for every generated-file gate |
| `hack/magusfile/index.buzz` | each project's MAGUS.md routing index, which the root index links |
| `hack/magusfile/toolchain.buzz` | installed toolchain versions against the ones upstream tagged |
| `hack/magusfile/toolchain-policy.buzz` | the version windows the workspace requires of the binaries its spells drive |

## lint/

`magus buzz hack/lint/run.buzz -- [--rule <name>] [<path>...]` runs every rule, one rule,
or only the findings at the named files. Each other file is a rule named for the sentence
it holds true, except `support.buzz`, which the rules share.

## docs/

`hack/docs/screenshots.sh` assembles the site, serves it and captures each committed
screenshot through `hack/docs/screenshot.mjs`.
