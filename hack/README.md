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
reports how long, writing nothing in the tree. `check` replays calls through what the
workspace wires and reports what was not refused, writing nothing in the tree. `rename`, `split`, `mark`, `prune`,
`merge` and `resolve` print a plan and write only with `--apply`. `bootstrap` prepares the checkout it
runs in, writing there and to its job's row only. `on-<where>` runs the command after it
somewhere else. The first line a
script prints says what it did, and the first line of `--help` says what it costs.
`hack/lint/hack-scripts-start-with-a-verb.buzz` holds every script to that and this file
to listing each one; `hack/lint/magusfile-modules-have-no-main.buzz` keeps modules and
scripts in their directories; `hack/lint/hack-scripts-parse-argv-with-flags.buzz` has every
script read its argv with `flags\parse`.

Each script's work is an exported, typed function that `main` only calls, so the
scripts are also reference to copy: read one before reaching for python, sed or jq.

## By task

Find the script by what you are doing; each row's flags are in its row under `dev/`.

| you want to | run |
| --- | --- |
| babysit open pull requests to main: action, gate-ready, refresh, gate, land | `hack/ci/pull-requests.buzz`, then `hack/dev/bootstrap-checkout.buzz --gate-ready`, `hack/dev/rebase-branches-onto-base.buzz` or `hack/dev/merge-base-into-branches.buzz`, `hack/dev/run-gate.buzz`, `hack/dev/land-commits.buzz` (the `babysit-pull-requests` skill) |
| make a checkout gate-ready: `./magus` at HEAD, pools, stale skill installs, graph, symbol digest | `hack/dev/bootstrap-checkout.buzz --gate-ready` |
| run the gate and see what failed | `hack/dev/run-gate.buzz` |
| read a failed Go test run as `file:line: message` | `hack/dev/ls-test-failures.buzz` |
| free disk, or clear finished worktrees | `hack/dev/prune-worktrees.buzz` |
| see every worktree, its uncommitted files and unpushed commits | `hack/dev/ls-worktrees.buzz` |
| see uncommitted files by owning project | `hack/dev/ls-uncommitted.buzz` |
| merge job branches or a split stack | `hack/dev/merge-job-branches.buzz` |
| open a pull request from commits, with CI's hygiene checks run first | `hack/dev/land-commits.buzz` |
| bring stacked branches up to date, in order, by merging the base into each | `hack/dev/merge-base-into-branches.buzz` |
| recover a pull request whose base pull request squash-merged | `hack/dev/rebase-branches-onto-base.buzz` |
| split the working change into stacked branches | `hack/dev/split-into-branches.buzz` |
| settle a file's merge conflict hunks | `hack/dev/resolve-conflicts.buzz` |
| build `./magus` in a checkout, or restart the daemon on it | `hack/dev/bootstrap-checkout.buzz` |
| set up a job's worktree | `hack/dev/bootstrap-worktree.buzz` |
| install the wired harnesses' skills and config | `hack/dev/bootstrap-harnesses.buzz` |
| rename a symbol everywhere | `hack/dev/rename-symbol.buzz` |
| read a worker's brief, or a session's cost, transcript or figure | `hack/dev/render-brief.buzz`, `hack/dev/summarize-root-cost.buzz`, `hack/dev/summarize-transcript.buzz`, `hack/dev/show-session-figure.buzz` |
| resume a session that hit a limit or crashed | `hack/dev/show-interrupted-session.buzz` |
| see what the guard refused, and mark the verdicts | `hack/dev/count-refusals.buzz`, `hack/dev/show-feedback.buzz`, `hack/dev/mark-feedback.buzz`, `hack/dev/count-feedback.buzz` |
| check the hooks still block what they should | `hack/dev/check-guard-blocks.buzz` |
| see which processes the OS killed under memory pressure | `hack/dev/show-memory-kills.buzz` |
| review a change with its callers, callees and tests | `hack/dev/show-review-context.buzz` |
| compare two trees | `hack/dev/diff-dirs.buzz` |

## dev/

| script | effect | cost |
| --- | --- | --- |
| `magus buzz hack/dev/rename-symbol.buzz -- --symbol <name> --to <new> [--apply]` | renames a code symbol at every occurrence the index verifies; dry run by default | needs the code index |
| `magus buzz hack/dev/split-into-branches.buzz -- [--by project\|symbols] [--base <rev>] [--prefix <branch>] [--budget <lines>] [-o json] [--apply]` | splits the working change into stacked branches, one per owning project or packed by changed declaration; dry run by default, never pushes | grows with tree size; `--by symbols` also reads the symbol index once per changed symbol |
| `magus buzz hack/dev/merge-job-branches.buzz -- (--jobs <id,...> \| --branches <name,...> \| --stack <file\|->) [--base <rev>] [--into <branch>] [-o json] [--apply] [--verify]` | merges job branches, named branches or a split-into-branches stack in a temporary worktree, settling generated files by regenerating; dry run by default, never pushes | a temporary worktree and one merge per branch; `--apply` also regenerates once, `--verify` runs every merged job's checks |
| `magus buzz hack/dev/prune-worktrees.buzz -- [--keep-recent <duration>] [--base <rev>] [--go-cache] [-o json] [--apply]` | plans which git worktrees and agent branches can go, largest first, with every reason a kept one stays; dry run by default | a git status and rev-list per worktree, one lsof, and a du per removable worktree |
| `magus buzz hack/dev/prune-changelog-fragments.buzz -- [-o json] [--apply]` | plans which unreleased changelog fragments are superseded, obsolete or duplicate from the history of the lines their commits wrote; dry run by default | a git blame per changed path, then a nested magus diff of about 13s per duplicate candidate |
| `magus buzz hack/dev/resolve-conflicts.buzz -- --file <path> [--take <n\|all>=ours\|theirs\|both\|base]... [--text <n>=<file>]... [--apply]` | lists a file's merge conflict hunks, or resolves the chosen ones and prints the diff; refuses an empty result or stray markers, writes through a same-directory rename, and only with `--apply` | none; reads and writes one file |
| `magus buzz hack/dev/land-commits.buzz -- --branch <name> --from <sha,...\|ref> --title <text> --body-file <file> [--dry-run]` | refuses a branch name taken locally or on origin, switches to it from a fresh origin/main, cherry-picks the commits, makes the checkout gate-ready, runs the pr-title, pr-description and pr-changelog targets, then pushes and runs `gh pr create`; stops at the first failed step, never enables auto-merge; `--dry-run` prints the steps | a fetch, three targets, a push |
| `magus buzz hack/dev/merge-base-into-branches.buzz -- --branches <a,b,c> [--base <ref>] [--dry-run]` | per branch in order: switches to it, makes it gate-ready, runs `magus vcs resolve --against` the previous branch (the first, `--base`, default origin/main), commits the merge and pushes; stops at the first source conflict and lists the files; refuses a dirty tree | one merge, one regeneration and a push per branch |
| `magus buzz hack/dev/rebase-branches-onto-base.buzz -- --branches <a,b,c> [--base <ref>] [--dry-run]` | for branches still carrying a squash-merged pull request's commits: finds the newest merged head the first branch carries (`gh pr list --state merged`), forks a `job fork --checkout` row, and per branch runs `git rebase --onto` the base (the first) or the rebased branch below, takes the replayed side of a generated conflict, makes the checkout gate-ready, runs `magus affected ci` with the checkout's own ./magus, commits its regeneration and pushes with `--force-with-lease`; stops at the first source conflict, aborts the rebase and lists the files; `--dry-run` prints the steps | a fetch, a gh query, then a rebase, the gate and a push per branch |
| `magus buzz hack/dev/example-typed-results.buzz -- [--file <path>] [--query <terms>] [--symbol <name>]` | read-only: magus's results as typed Buzz values, where a shell pipes `-o json` into jq | needs the code index |
| `magus buzz hack/dev/ls-uncommitted.buzz` | read-only: uncommitted files by owning project and declared role | grows with tree size |
| `magus buzz hack/dev/ls-worktrees.buzz -- [--dirty]` | read-only: every worktree with its uncommitted files, unpushed commits and job | grows with the number of worktrees |
| `magus buzz hack/dev/run-gate.buzz -- [--timeout <duration>] [--slots <n>] [--wait <seconds>] [--lines <n>]` | makes the checkout gate-ready, then runs `magus affected ci` under `job exec --timeout` once the broker has the slots free (polling `broker status` up to `--wait` seconds), then prints each failed member's output ref and its first failing lines; the guard judges it as the gate | the whole gate, plus the seat wait |
| `magus buzz hack/dev/ls-test-failures.buzz -- [--ref <output-ref>]` | read-only: a failed Go test run as `file:line: message` lines | none |
| `magus buzz hack/dev/show-interrupted-session.buzz -- [--host <host>] [--session <id>]` | read-only: a session that hit a usage limit or crashed, its checkout's state, and the commands that carry its work here | none |
| `magus buzz hack/dev/show-memory-kills.buzz -- [--since <duration\|timestamp>] [--until <timestamp>] [--name <substring>] [--all]` | read-only: the processes the OS killed or suspended under memory pressure in a window, and the swap state around them | none; reads the OS log |
| `magus buzz hack/dev/count-symbols.buzz -- [--project <path>] [--kind <kind>]` | read-only: a project's code symbols by kind and by the first word of their names | needs the code index |
| `magus buzz hack/dev/count-refusals.buzz` | read-only: this checkout's recurring guard refusals by rule | none |
| `magus buzz hack/dev/check-guard-blocks.buzz` | writes nothing in the tree: replays a canonical set of tool calls through the hooks each host `magus describe harness` names (claude-code, codex, cursor) wires here, in that host's own event and reply format, and reports per host every call the guard answered weaker than expected; every case runs, a host with nothing wired is a row, and it exits non-zero at the end on any weak row | one hook process per call per host, about a second each |
| `magus buzz hack/dev/show-feedback.buzz -- [--session <id>] [--since <duration\|RFC3339>] [--until <RFC3339>] [--page <n>] [-o json]` | read-only: one session's guard feedback, ten labelled rows a page: refused, advised, unguarded command shapes, served nexts not taken; each page ends with the command for the next | every checkout's trail that changed in the window |
| `magus buzz hack/dev/mark-feedback.buzz -- --verdict should-deny\|should-advise\|wrong-deny\|fine [--note <text>] [--session <id>] [--since ...] [--until ...] [--apply] -- <label\|id>...` | records a verdict on feedback rows under their stable ids in the per-repository store; dry run by default | as show-feedback |
| `magus buzz hack/dev/count-feedback.buzz -- [--since <duration>] [-o json]` | read-only: every mark across sessions folded into a ranked guard backlog | none |
| `magus buzz hack/dev/show-session-figure.buzz -- [--session <id>] [--since ...] [--all] [-o svg] [--theme light\|dark] [--console <base>]` | read-only: a session's job hierarchy as a summary and the console link that explores it; `-o svg` prints the figure for you to redirect | every checkout's trail that changed in the window, and the job store |
| `magus buzz hack/dev/bootstrap-worktree.buzz -- (--job <id> [--from <checkout> \| --build-from <checkout>] \| --check --from <checkout>)` | places the orchestrator's ./magus (builds one only under `--build-from`), refreshes the wired harness skills, builds the graph, then takes the job's lease; refuses unless the row names this worktree and its checkpoint. `--from` (default: the main checkout) names the checkout whose ./magus it copies, only when no Go build input differs from the commit that binary was built at; otherwise it refuses and prints the one build to run in the root. `--build-from` runs `<checkout>/magus --root <checkout> run go-build .` first and is refused under a job lease. `--check` prints that verdict and writes nothing | a graph build; seconds for the copy |
| `magus buzz hack/dev/bootstrap-checkout.buzz -- (--checkout <dir> \| --here) [--restart \| --gate-ready]` | `--gate-ready` makes the checkout ready to gate instead and prints what it did: builds ./magus only when its version is not HEAD, runs `./magus server stop --pools`, removes each untracked skill install under `.claude/skills`, `.agents/skills` or `.opencode/skills` that `magus agent install` stamped (never a tracked file), then `./magus graph build` and `./magus run symbol-digest-generate .`; idempotent. run-gate, rebase-branches-onto-base, merge-base-into-branches and land-commits run it before they gate. Otherwise builds ./magus in a job's checkout at the checkout's own revision, for a fork made at a checkpoint no built binary matches: runs `GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .` there and checks that `<dir>/magus version` names its HEAD; refuses a checkout holding uncommitted changes. `--here` builds in the current checkout. `--restart` stops and starts the daemon with the new binary and checks `version` names HEAD. It runs `mise trust <dir>/mise.toml` first when that file exists and mise is on PATH The guard lets the root and the job holding that checkout run it | two Go compiles of magus |
| `magus buzz hack/dev/bootstrap-harnesses.buzz -- [--id <harness>]` | installs the wired harnesses' skills, then merges each one's host config as `magus describe harness` plans it (content written whole, a fragment deep-merged or written new); prints a line per file changed or `no change`; refuses, naming the files, while `spells/harness/`, `docs/guides/integrations/agents/` or `hack/policy/` differ from HEAD. `bootstrap-worktree.buzz` runs it when no lease is bound | one skill install and a plan read per harness |
| `magus buzz hack/dev/render-brief.buzz -- --job <id> [--extra <file>]` | read-only: a worker's complete brief from its job row | two job-store reads |
| `magus buzz hack/dev/summarize-transcript.buzz -- [--match <pattern>] -- <file>...` | read-only: a session transcript's calls per tool, refusals by rule, retries, followed suggestions and tokens | grows with transcript size |
| `magus buzz hack/dev/summarize-root-cost.buzz -- [--all]` | read-only: the cost section of `magus session show` rolled up across the sessions this checkout loaded, each total saying how many sessions measured it | none |
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

For GitHub CI failures, select the pull request's current head with `--commit`
or its run with `--run` in `show-failures.buzz`, then pipe its records to
`magus query output --stdin`. PR status summaries and raw job logs do not
isolate the captured output for each failed target. A run without retained
failure artifacts is reported as such; read its raw job logs with
`gh run view <run-id> --log-failed` to diagnose setup failures and jobs or workflows
without target artifacts. Missing artifacts do not mean a passing run.

| script | does | cost |
| --- | --- | --- |
| `hack/ci/merge-queue.buzz` | the merge queue's workflow steps, one subcommand each | network |
| `magus buzz hack/ci/pull-requests.buzz -- status \| apply \| dashboard \| boxes \| act` | named before the verb rule: each open pull request's state and the action it needs; `apply` queues and reruns; the queue workflows render `dashboard`, and queue-dashboard.yaml answers its boxes with `boxes` and `act` | network |
| `magus buzz hack/ci/show-failures.buzz -- [--commit <rev>] [--run <id>] \| magus query output --stdin` | read-only: writes the output records of a commit's failed CI targets, from the run's `magus-logs-*` artifacts, to stdout for `magus query output --stdin` to print; exits 3 when the commit has no CI run | network |
| `magus buzz hack/ci/labels.buzz -- [--repo <owner/name>]` | named before the verb rule: prints the `gh label` commands for this repository's label taxonomy | none |

`ci/completion-checks/` holds the shell scripts the root `completion-test` target runs in
each shell's official image.

## magusfile/

Targets import these; each runs through its target rather than by hand.

| module | holds |
| --- | --- |
| `hack/magusfile/advisories.buzz` | advisory scanning that fails only on findings you can act on today |
| `hack/magusfile/badges.buzz` | the coverage badge SVG, in the GitHub Actions badge's palette |
| `hack/magusfile/branch-stack.buzz` | the typed BranchStack record split-into-branches emits and merge-job-branches reads, with its strict reader, writer and layer ordering |
| `hack/magusfile/changelog.buzz` | the changelog fragments' one grammar and renderer |
| `hack/magusfile/commits.buzz` | the conventional-commit rule the pull request title check and the commit hook share |
| `hack/magusfile/conformance-ratchet.buzz` | the prune `format` runs over `hack/policy/conformance.jsonl`: it deletes the known items that no longer violate, and only those, and only when every symbol index is already current |
| `hack/magusfile/coverage.buzz` | the Go coverage profile filtered to hand-written code, and the static statement count the published figure divides by |
| `hack/magusfile/drift.buzz` | drift measured by content, for every generated-file gate |
| `hack/magusfile/harness.buzz` | which committed host config files lack what their wired harness spell renders, and the merge command for each |
| `hack/magusfile/index.buzz` | each project's MAGUS.md routing index, which the root index links |
| `hack/magusfile/mockassert.buzz` | the compile-time assertions that each published mock still satisfies its interface, derived from `.mockery.yaml` |
| `hack/magusfile/releases.buzz` | which modules version independently, the versions each may move to, release tags, the release signature check and the release-index publish |
| `hack/magusfile/ruledocs.buzz` | the guard-rule reference: one page per rule the running binary enforces, plus the index |
| `hack/magusfile/run-golangci.buzz` | the custom golangci-lint build run with the workspace config, and the lib-module lint that shares it |
| `hack/magusfile/toolchain.buzz` | installed toolchain versions against the ones upstream tagged |
| `hack/magusfile/toolchain-policy.buzz` | the version windows the workspace requires of the binaries its spells drive |

## lint/

`magus buzz hack/lint/run.buzz -- [--rule <name>] [<path>...]` runs every rule, one rule,
or only the findings at the named files. Each other file is a rule named for the sentence
it holds true, except `support.buzz`, which the rules share.

## docs/

`hack/docs/screenshots.sh` assembles the site, serves it and captures each committed
screenshot through `hack/docs/screenshot.mjs`.
