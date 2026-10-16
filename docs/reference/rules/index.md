---
title: "Guard rules"
description: "Every rule the guard enforces: what each one catches, whether it denies or advises by default, and where the reasoning lives."
tags: [guard, rules, reference]
---

# Guard rules

What this workspace enforces. A **deny** refuses the call and names the
replacement; an **advise** attaches context and blocks nothing. The tables
show each rule's compiled default, and a workspace sets any of them with
`magus\guard.builtins`.

Every verdict names its rule in brackets (`deny [stage-all]: ...`), and that
name is the entry below. `magus describe rules` prints the same list.

Rules for written text, such as pull request descriptions and docs, are the
[proofread rules](../proofread/index.md).

## Denies by default

| Rule                                                  | Catches                                                                                              |
| ----------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| [agent-sign-off](agent-sign-off.md)                   | an agent stamping a read receipt or closing an attention request, which only a person may do         |
| [background-left](background-left.md)                 | an agent finishing while shell commands it ran in the background are still running                   |
| [background-unbounded](background-unbounded.md)       | a shell command run in the background with nothing to end it                                         |
| [backtick-substitution](backtick-substitution.md)     | a backtick command substitution, which inside double quotes runs a command                           |
| [busy-wait](busy-wait.md)                             | a while or until loop that sleeps, or a for loop that sleeps until it breaks out                     |
| [cache-dir-write](cache-dir-write.md)                 | a write into this checkout's magus cache dir, which magus alone owns                                 |
| [claimed-declaration](claimed-declaration.md)         | a leased edit landing in a declaration another live job claims (`run.go#executeStages`)              |
| [conflict-rewrite](conflict-rewrite.md)               | an inline interpreter rewriting a file that holds merge conflict markers                             |
| [credential-verb](credential-verb.md)                 | an agent minting, printing, rotating or revoking a credential through the CLI                        |
| [focus-read](focus-read.md)                           | a read outside the paths a focus lease was given                                                     |
| [heredoc-write](heredoc-write.md)                     | a here-document written to a file, judged as the file write it is                                    |
| [hook-wiring-write](hook-wiring-write.md)             | a leased or agent-attributed write to the hook wiring the guard is installed by                      |
| [hooks-off](hooks-off.md)                             | a git call with the repository's hooks switched off                                                  |
| [human-only](human-only.md)                           | a magus run reaching a human_only target: the person is asked, a leased worker refused               |
| [inline-alias](inline-alias.md)                       | a VCS alias defined inline (`git -c alias.x=...`), which hides the command it runs                   |
| [lease-gate](lease-gate.md)                           | a leased worker running the gate instead of the check it was assigned                                |
| [lease-harness](lease-harness.md)                     | a leased worker rewriting the harness skill trees that steer it                                      |
| [lease-rebind](lease-rebind.md)                       | a leased worker rewriting who it is or what its own job row says                                     |
| [lease-undeclared](lease-undeclared.md)               | a call graded under a lease id the job store has no row for, or a binding it tombstoned              |
| [lease-vcs](lease-vcs.md)                             | a worker lease mutating version control, or committing outside its own branch and checkout           |
| [lease-write](lease-write.md)                         | a leased write outside its write paths, or into a path it was denied or another lease owns           |
| [merge-side-checkout](merge-side-checkout.md)         | a checkout of one merge side over a conflicted file, which discards the merge                        |
| [notes-author](notes-author.md)                       | an agent authoring a human's note, whose only provenance is who wrote it                             |
| [orchestrator-groundwork](orchestrator-groundwork.md) | a session that hands out work doing its own groundwork: reads, searches, edits, runs                 |
| [policy-unloaded](policy-unloaded.md)                 | a push, merge, spawn or state-writing magus verb while the guard policy that judges it does not load |
| [process-poll](process-poll.md)                       | a process table inspected to wait on magus work the lock already reports                             |
| [push-ungated](push-ungated.md)                       | a push at a commit with no green gate: the person is asked, a leased worker refused                  |
| [shared-stash](shared-stash.md)                       | a bare stash push or pop, on a stack every worktree shares                                           |
| [sleep-poll](sleep-poll.md)                           | a sleep before a status read, a call that only sleeps, or a wait on or retry into a full budget      |
| [spawn-above-parent-tier](spawn-above-parent-tier.md) | a subagent spawned on a model whose tier ranks above its spawner's                                   |
| [spawn-duplicate](spawn-duplicate.md)                 | a spawn for a job that already has a worker                                                          |
| [spawn-unvalidated](spawn-unvalidated.md)             | a spawn for a job whose record was never validated                                                   |
| [stale-binary](stale-binary.md)                       | a call that changes state while the magus judging it cannot load this tree's guard policy            |
| [token-state](token-state.md)                         | an agent reading or writing the token secrets: the operator token file or the token store            |
| [vcs-off-switch](vcs-off-switch.md)                   | an agent's write setting vcs.enabled: false in a magus.yaml this workspace reads                     |
| [whole-tree](whole-tree.md)                           | a whole-tree VCS reset, checkout, restore or clean, which cannot be undone                           |
| [worker-background-run](worker-background-run.md)     | a bound worker running a magus target in the background                                              |
| [worker-check-only](worker-check-only.md)             | a bound worker running a target other than its row's check or one writing its write paths            |
| [worktree-remove](worktree-remove.md)                 | removing a worktree magus cannot prove holds nothing that would be lost                              |

## Advises by default

| Rule                                                    | Catches                                                                                            |
| ------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| [architecture-unbriefed](architecture-unbriefed.md)     | an agent's next call after a structure question, or a new directory, before the architecture skill |
| [brief-command](brief-command.md)                       | a spawn or continuation brief that teaches a command the guard denies                              |
| [brief-plain-commands](brief-plain-commands.md)         | a brief for an isolated worker that does not hold it to one plain command per call                 |
| [buzz-unbriefed](buzz-unbriefed.md)                     | the first Buzz a session authors, by file write or `magus buzz -e`, before reading the Buzz skill  |
| [capture-filter](capture-filter.md)                     | a filter over a run capture or log, which cuts the failure block apart                             |
| [chained-run](chained-run.md)                           | magus runs sequenced with `&&` or `;`, which a pipe of the same stages runs ordered and fail-fast  |
| [check-timeout](check-timeout.md)                       | a bound worker's magus run whose timeout is shorter than the target's last real run allows for     |
| [checkpoint-state](checkpoint-state.md)                 | a command reaching for a tree's identity, which a revision alone cannot give                       |
| [compat-marker](compat-marker.md)                       | a leased worker adding a marker that keeps code for older data or callers                          |
| [dependency-install](dependency-install.md)             | a raw package install that the cached install target already runs                                  |
| [dependency-update](dependency-update.md)               | a raw dependency update outside a target's update charm                                            |
| [echo-on-success](echo-on-success.md)                   | an `&& echo` that restates what the exit status already says                                       |
| [exit-status-echo](exit-status-echo.md)                 | a line ending by printing an exit status, which the harness already reports                        |
| [filter-without-input](filter-without-input.md)         | a filter with no file, pipe or redirect, which reads a stdin nothing feeds                         |
| [focus](focus.md)                                       | a read or write outside the paths the running job declared                                         |
| [gate-repeat](gate-repeat.md)                           | the gate run a third time over a tree its last green verdict already covers                        |
| [generated-write](generated-write.md)                   | a hand edit to a declared output, which the next run overwrites                                    |
| [graph-pipe](graph-pipe.md)                             | a read-only graph verb piped into a text filter, when magus projects the record itself             |
| [graph-stale](graph-stale.md)                           | a graph read, or a graph-backed deny, while the graph describes another tree                       |
| [grep-reader](grep-reader.md)                           | a definition lookup with a context flag (`grep -A40 'func X'`), which uses grep to read the body   |
| [guard-unfinished](guard-unfinished.md)                 | guard checks that never finished, so no rule judged their calls                                    |
| [hook-wiring](hook-wiring.md)                           | a write to the host wiring that decides whether these rules run at all                             |
| [installed-skill](installed-skill.md)                   | a write to an installed skill copy, which re-installing discards                                   |
| [instruction-write](instruction-write.md)               | a write to a cross-host instruction file, which every session loads whole                          |
| [interpreter-rewrite](interpreter-rewrite.md)           | an inline interpreter rewriting a file this tree already carries                                   |
| [lease-invalid](lease-invalid.md)                       | a call naming a lease this workspace's job store does not declare                                  |
| [lease-state](lease-state.md)                           | a leased write while its row reports a diverged base, a re-entered path, or a bad pattern          |
| [lease-terminal](lease-terminal.md)                     | a call naming a lease whose row has already finished                                               |
| [leased-path](leased-path.md)                           | a write into paths a running lease owns, by a caller that names no lease                           |
| [magus-timeout](magus-timeout.md)                       | a magus call wrapped in coreutils `timeout` or `gtimeout`, which kills it from outside             |
| [model-tier-context-cap](model-tier-context-cap.md)     | a leased worker writing past the context its job's model tier caps                                 |
| [monitor-duplicate](monitor-duplicate.md)               | a second live watcher on one subject, or one on work the agent backgrounded itself                 |
| [monitor-pile](monitor-pile.md)                         | an agent arming a waiter while several of its own are already live                                 |
| [new-file](new-file.md)                                 | a new file in a directory whose naming has settled                                                 |
| [new-source-dir](new-source-dir.md)                     | a new file that opens a directory, which is a boundary rather than a file                          |
| [output-pipe](output-pipe.md)                           | magus output piped into a filter, when magus projects the record itself                            |
| [output-redirect](output-redirect.md)                   | magus output sent to a file or discarded, which the run log already holds                          |
| [precedent-search](precedent-search.md)                 | a hunt for one distinctive name, which refs answers with verified sites                            |
| [push-gate](push-gate.md)                               | a push the run log does not prove ungated, which names the gate and lets it through                |
| [raw-tool](raw-tool.md)                                 | a toolchain command a spell already wraps, run outside the cache                                   |
| [read-navigation](read-navigation.md)                   | a whole read of a Go, Buzz or Markdown file past the workspace's line limit                        |
| [read-paging](read-paging.md)                           | a mapped file read window by window, past two windows of it                                        |
| [read-symbol](read-symbol.md)                           | a bounded read inside one indexed declaration, which refs --definition --source prints checked     |
| [regen-source](regen-source.md)                         | a hand edit to a file a target regenerates                                                         |
| [release-requested](release-requested.md)               | a job holding write paths another fork was refused for, with a release request open                |
| [revert-classify](revert-classify.md)                   | a revert that has not classified what it is reverting                                              |
| [scope-drift](scope-drift.md)                           | a write into a project this session has no dependency edge to                                      |
| [scripted-rewrite](scripted-rewrite.md)                 | a scripted substitute-and-write, which cannot tell your symbol from a dependency's                 |
| [search-translation](search-translation.md)             | a text search whose pattern a graph query provably answers with the same entities                  |
| [sed-in-place](sed-in-place.md)                         | `sed -i`, whose two spellings destroy each other's work across platforms                           |
| [shared-checkout](shared-checkout.md)                   | a spawn into a checkout a live job already holds and writes                                        |
| [sibling-checkout](sibling-checkout.md)                 | a magus command relocated into another checkout, judging a tree nobody ships                       |
| [skill-source](skill-source.md)                         | a write to an installed skill copy rather than to its source                                       |
| [source-read](source-read.md)                           | an unbounded source read the symbol index has already answered                                     |
| [spawn-nested-waiter](spawn-nested-waiter.md)           | a subagent spawning a helper whose title or brief opening speaks of waiting or watching            |
| [spawn-unbriefed](spawn-unbriefed.md)                   | a subagent spawned before the multi-agent skill loaded                                             |
| [spawn-unplaced](spawn-unplaced.md)                     | a subagent's first call that no waiting spawn names, which then runs under no lease                |
| [spawn-waiter](spawn-waiter.md)                         | a spawn whose child exists to wait on, poll or watch other work                                    |
| [split-run](split-run.md)                               | the same target run again on a different project set, as a separate call                           |
| [stage-all](stage-all.md)                               | a whole-tree `git add` (-A, -u, ., --all, --update), which sweeps in regenerated output            |
| [stage-classify](stage-classify.md)                     | staging without classifying, when generated and source differ                                      |
| [symbol-search](symbol-search.md)                       | a text search of the tree for a symbol, a declaration or a diagnostic code the graph answers       |
| [throwaway-copy](throwaway-copy.md)                     | a run inside a temp or scratchpad copy, which leaves the real tree unverified                      |
| [timed-magus](timed-magus.md)                           | `time` around a silent magus run, which already reports its own durations                          |
| [unjudged-tool](unjudged-tool.md)                       | a tool call whose input carries nothing any rule reads, from a tool the guard does not know        |
| [unknown-env](unknown-env.md)                           | a retired or misspelled MAGUS_* variable handed to a command                                       |
| [unleased-write](unleased-write.md)                     | a write magus cannot attribute while a fleet is running                                            |
| [workspace-command-failed](workspace-command-failed.md) | a command the workspace's rule could not judge, so only the built-in rules graded it               |
| [workspace-spawn-failed](workspace-spawn-failed.md)     | a spawn the workspace's rule could not judge, so only the built-in rules graded it                 |
| [workspace-write-failed](workspace-write-failed.md)     | a file write the workspace's rule could not judge, so only the built-in rules graded it            |
| [root-context-budget](root-context-budget.md)           | a session's main thread whose context has grown past its level's size                              |
| [root-heavy-command](root-heavy-command.md)             | a main thread running delegable commands one after another without spawning                        |
| [root-repeat](root-repeat.md)                           | a main thread running the same delegable command again with no write since                         |
| [root-result-cap](root-result-cap.md)                   | one tool result too large for a main thread's context, said on its next call                       |
| [spawn-above-model-tier](spawn-above-model-tier.md)     | a subagent spawned on a model whose tier ranks above the tier its job was forked at                |
| [spawn-names-model](spawn-names-model.md)               | a main thread spawning a subagent without naming the model it runs on                              |
| [stall](stall.md)                                       | a leased worker refused by something only the root can clear                                       |
