---
title: "Guard rules"
description: "Every rule the guard enforces: what each one catches, whether it refuses or explains, and where the reasoning lives."
tags: [guard, rules, reference]
---

# Guard rules

What this workspace enforces. A **deny** refuses the call and names the
replacement; an **advise** attaches context and blocks nothing.

Every verdict names its rule in brackets (`deny [stage-all]: ...`), and that
name is the entry below. `magus describe rules` prints the same list.

## Refuses

| Rule                                          | Catches                                                                                           |
| --------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| [busy-wait](busy-wait.md)                     | a loop polling for work you started, which announces its own completion                           |
| [buzz-unbriefed](buzz-unbriefed.md)           | the first Buzz a session authors, by file write or `magus buzz -e`, before reading the Buzz skill |
| [cache-dir-write](cache-dir-write.md)         | a write into this checkout's magus cache dir, which magus alone owns                              |
| [capture-filter](capture-filter.md)           | a filter over a run capture or log, which cuts the failure block apart                            |
| [cd](cd.md)                                   | a `cd` before a magus command, when the project is an argument                                    |
| [credential-verb](credential-verb.md)         | an agent minting, printing, rotating or revoking a credential through the CLI                     |
| [exit-status-echo](exit-status-echo.md)       | a trailing `echo $?`, which repeats an exit status the harness already reports                    |
| [interpreter-rewrite](interpreter-rewrite.md) | an inline interpreter rewriting a file this tree already carries                                  |
| [merge-side-checkout](merge-side-checkout.md) | a checkout of one merge side over a conflicted file, which discards the merge                     |
| [notes-author](notes-author.md)               | an agent authoring a human's note, whose only provenance is who wrote it                          |
| [output-pipe](output-pipe.md)                 | magus output piped into a filter, when magus projects the record itself                           |
| [output-redirect](output-redirect.md)         | magus output redirected to a file, which the run log already holds                                |
| [person-only](person-only.md)                 | an agent stamping a read receipt or closing an attention request, which only a person may do      |
| [process-poll](process-poll.md)               | a process table inspected to wait on magus work the lock already reports                          |
| [push-ungated](push-ungated.md)               | a push at a commit with no green gate: the person is asked, a leased worker refused               |
| [raw-tool](raw-tool.md)                       | a toolchain command a spell already wraps, run outside the cache                                  |
| [scripted-rewrite](scripted-rewrite.md)       | a scripted substitute-and-write, which cannot tell your symbol from a dependency's                |
| [sed-in-place](sed-in-place.md)               | `sed -i`, whose two spellings destroy each other's work across platforms                          |
| [shared-stash](shared-stash.md)               | a bare stash push or pop, on a stack every worktree shares                                        |
| [sibling-checkout](sibling-checkout.md)       | a magus command relocated into another checkout, judging a tree nobody ships                      |
| [spawn-unbriefed](spawn-unbriefed.md)         | a subagent spawned before the multi-agent skill loaded                                            |
| [stage-all](stage-all.md)                     | a whole-tree `git add` (-A, -u, ., --all, --update), which sweeps in regenerated output           |
| [symbol-search](symbol-search.md)             | a recursive text search for a symbol the index defines and can enumerate                          |
| [throwaway-copy](throwaway-copy.md)           | a run inside a temp or scratchpad copy, which leaves the real tree unverified                     |
| [token-state](token-state.md)                 | an agent reading or writing the token secrets: the operator token file or the token store         |
| [whole-tree](whole-tree.md)                   | a whole-tree VCS reset, checkout, restore or clean, which cannot be undone                        |
| [worktree-remove](worktree-remove.md)         | removing a worktree, which may hold another session's uncommitted work                            |

## Explains

| Rule                                    | Catches                                                                                 |
| --------------------------------------- | --------------------------------------------------------------------------------------- |
| [chained-run](chained-run.md)           | several magus runs chained on one line, where the dependency graph would have run them  |
| [checkpoint-state](checkpoint-state.md) | a command reaching for a tree's identity, which a revision alone cannot give            |
| [code-search](code-search.md)           | a repo-wide text search that the symbol graph may answer better                         |
| [doc-search](doc-search.md)             | a search through markdown, where headings are indexed as doc sections                   |
| [focus](focus.md)                       | a read or write outside the paths the running job declared                              |
| [gate-repeat](gate-repeat.md)           | the gate run again soon after it passed, repeating work already done                    |
| [generated-write](generated-write.md)   | a hand edit to a declared output, which the next run overwrites                         |
| [graph-stale](graph-stale.md)           | a graph read while the index is older than the sources it describes                     |
| [hook-wiring](hook-wiring.md)           | a write to the host wiring that decides whether these rules run at all                  |
| [installed-skill](installed-skill.md)   | a write to an installed skill copy, which re-installing discards                        |
| [lease-invalid](lease-invalid.md)       | a call naming a lease this workspace's job store does not declare                       |
| [lease-terminal](lease-terminal.md)     | a call naming a lease whose row has already finished                                    |
| [memory-write](memory-write.md)         | a write to a memory file, where the memory surface is the way in                        |
| [new-file](new-file.md)                 | a new file in a directory whose naming has settled                                      |
| [new-source-dir](new-source-dir.md)     | a new file that opens a directory, which is a boundary rather than a file               |
| [precedent-search](precedent-search.md) | a hunt for one distinctive name, which refs answers with verified sites                 |
| [push-gate](push-gate.md)               | a push the run log does not prove ungated, which names the gate and lets it through     |
| [regen-source](regen-source.md)         | a hand edit to a file a target regenerates                                              |
| [revert-classify](revert-classify.md)   | a revert that has not classified what it is reverting                                   |
| [scope-drift](scope-drift.md)           | a write into a project this session has no dependency edge to                           |
| [skill-source](skill-source.md)         | a write to an installed skill copy rather than to its source                            |
| [source-read](source-read.md)           | an unbounded source read the symbol index has already answered                          |
| [split-run](split-run.md)               | the same target run again on a different project set, on one line or as a separate call |
| [stage-classify](stage-classify.md)     | staging without classifying, when generated and source differ                           |
| [unleased-write](unleased-write.md)     | a write magus cannot attribute while a fleet is running                                 |
