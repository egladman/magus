# hack/

Buzz scripts for working on this repository. Run one with `magus buzz`, passing its
flags after `--`:

```sh
magus buzz hack/<script>.buzz -- --help
```

A script's name starts with what running it does. `ls`, `show`, `count`, `diff`,
`summarize` and `example` only read. `rename` and `split` print a plan and write only
with `--apply`. `on-<where>` runs the command after it somewhere else. The first line a
script prints says what it did, and the first line of `--help` says what it costs.
`hack/lint/hack-scripts-start-with-a-verb.buzz` holds every script here to that, and
this file to listing each one.

Each script's work is an exported, typed function that `main` only calls, so the
scripts are also reference to copy: read one before reaching for python, sed or jq.

## Run a command somewhere else

The command after `--` runs there. Its stdout, stderr and exit code are its own.

| script | runs the command | cost |
| --- | --- | --- |
| `magus buzz hack/on-actions.buzz -- <command>` | on a GitHub Actions runner: proves something on Linux without a pull request | network; pushes a scratch branch and spends CI minutes |
| `magus buzz hack/on-linux.buzz -- <command>` | in a local Linux container, through Podman | a local container, plus an image pull the first time |

## Scripts you run

| script | effect | cost |
| --- | --- | --- |
| `magus buzz hack/rename-symbol.buzz -- --symbol <name> --to <new> [--apply]` | renames a code symbol at every occurrence the index verifies; dry run by default | needs the code index |
| `magus buzz hack/split-into-branches.buzz -- [--base <rev>] [--prefix <branch>] [--apply]` | splits the working change into stacked branches, one per owning project; dry run by default, never pushes | grows with tree size |
| `magus buzz hack/example-typed-results.buzz -- [--file <path>] [--query <terms>] [--symbol <name>]` | read-only: magus's results as typed Buzz values, where a shell pipes `-o json` into jq | needs the code index |
| `magus buzz hack/ls-uncommitted.buzz` | read-only: uncommitted files by owning project and declared role | grows with tree size |
| `magus buzz hack/ls-worktrees.buzz -- [--dirty]` | read-only: every worktree with its uncommitted files, unpushed commits and job | grows with the number of worktrees |
| `magus buzz hack/ls-test-failures.buzz -- [--ref <output-ref>]` | read-only: a failed Go test run as `file:line: message` lines | none |
| `magus buzz hack/show-interrupted-session.buzz -- [--host <host>] [--session <id>]` | read-only: a session that hit a usage limit or crashed, its checkout's state, and the commands that carry its work here | none |
| `magus buzz hack/count-symbols.buzz -- [--project <path>] [--kind <kind>]` | read-only: a project's code symbols by kind and by the first word of their names | needs the code index |
| `magus buzz hack/count-refusals.buzz` | read-only: this checkout's recurring guard refusals by rule | none |
| `magus buzz hack/summarize-transcript.buzz -- [--match <pattern>] -- <file>...` | read-only: a session transcript's calls per tool, refusals by rule, retries, followed suggestions and tokens | grows with transcript size |
| `magus buzz hack/diff-dirs.buzz -- --a <dir> --b <dir> [--unified]` | read-only: two trees compared file by file, declared outputs left out | grows with tree size |
| `magus buzz hack/toolchain-releases.buzz -- fetch \| build \| verify` | builds the signed toolchain end-of-life data from endoflife.date | fetch uses the network; build and verify are local |

Named before the verb rule, each due a verb name:

| script | effect | cost |
| --- | --- | --- |
| `magus buzz hack/pull-requests.buzz -- status \| apply` | reports each open pull request's state and the action it needs; `apply` queues and reruns | network |
| `magus buzz hack/labels.buzz -- [--repo <owner/name>]` | read-only: prints the `gh label` commands for this repository's label taxonomy | none |
| `magus buzz hack/host-schemas.buzz` | reads, and on request refreshes, the vendored agent-host schemas | network |

## Libraries the magusfile imports

Targets in `magusfile.buzz` import these; each runs through its target rather than by
hand.

| library | holds |
| --- | --- |
| `hack/advisories.buzz` | advisory scanning that fails only on findings you can act on today |
| `hack/changelog.buzz` | the changelog fragments' one grammar and renderer |
| `hack/commits.buzz` | the conventional-commit rule the pull request title check and the commit hook share |
| `hack/drift.buzz` | drift measured by content, for every generated-file gate |
| `hack/toolchain.buzz` | installed toolchain versions against the ones upstream tagged |
| `hack/toolchain-policy.buzz` | the version windows the workspace requires of the binaries its spells drive |
| `hack/lint.buzz` and `hack/lint/` | this repository's file linter and its rules |

## CI steps (hack/ci/)

Run only by a CI workflow.

| step | does |
| --- | --- |
| `hack/ci/merge-queue.buzz` | drives the merge queue's workflow steps |

`policy/` holds this repository's guard rules, `git-hooks/` its git hooks, and
`testdata/` and `testdata-upstream/` the fixtures the scripts' tests read.
