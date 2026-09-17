---
title: "Git is not the bottleneck"
description: "A take going around says Git will not survive agents: three people, ninety pull requests a day, twenty-minute checks, twelve clones per laptop. Every number in it is real. None of them is Git. The scarce thing is a trustworthy verdict, and here is what that costs in this repository, measured today."
tags: [opinion]
date: 2026-09-15
draft: true
---

# Git is not the bottleneck

There is a take going around that Git is one of the things agents will kill.
The evidence offered: a three-person team, each opening twenty to thirty pull
requests a day; checks that take fifteen to twenty minutes; a linear-history
rule, so every merge forces the next branch to rebase and start its checks
over; a ceiling of about fifty merges a day; and twelve clones of one repository
on one laptop so twelve agents can work at once. Conclusion: this process was
not built for AI coding, and someone will build the new Git.

I want to take the complaint seriously, because the pain is real, and then show
that the arithmetic points somewhere else.

## What is true in it

The process really was not built for this. A pull request used to cost hours of
a person's attention, and a twenty-minute check on the end of it was noise. When
producing a branch costs minutes, the check is most of the cost, and a queue
that serializes on it stops moving. That part of the take is right, and it is
the argument the doctrine page makes under "optimize the loop that verifies,
never the loop that generates": producing code was never the bottleneck.

Two smaller things are also fair. Twelve clones is a failure of discovery, and
Git owns that: `git worktree` shipped in 2015 and shares one object store across
as many checkouts as you like, and a person who has used Git for a decade can
still not know it exists. And the merge-queue problem is not imaginary. Linear
history plus a serialized verdict is a real ceiling, and Git offers no first
class way to say "this tree was already verified."

## The arithmetic

Take the numbers as given. Fifty merges at seventeen and a half minutes each is
875 minutes, fourteen and a half hours of check time, serialized. A queue that
can admit one branch per verdict admits three and a half an hour, and over a
working day that spreads across time zones and evenings, that is roughly fifty.
The ceiling is the verdict length times the serialization. Nothing else in the
anecdote is close: a rebase is milliseconds, a merge is milliseconds, and sixty
to ninety authored branches a day are waiting on the fifty verdicts, not on the
ledger that stores them.

So the scarce resource is not commits per day. It is trustworthy verdicts per
day. Halve the verdict and the same queue doubles. Make the verdict on an
untouched project free and the re-check after a rebase shrinks to the
intersection of what the merged branch touched and what yours reaches.

Git already has the primitive this needs. Every directory in a Git tree is a
content-addressed object; two branches whose `services/billing` subtrees hash
the same have the same `services/billing`, whatever their commit ids say. The
check pipeline throws that away. It keys on the commit, the commit changes on
every rebase, and everything runs again. Replacing Git would not fix that. The
thing to replace is a verdict pipeline that ignores the content addressing Git
already gave it.

## What magus does about it

magus keys a target's cache entry on the content of the files the target
declares it reads, the arguments it was given, the tool versions, and the keys
of its upstream targets, and on nothing about the commit (`docs/concepts/cache.md`,
"The cache key"). A rebase that leaves a project's declared inputs byte-identical
replays that project's outputs and its verdict instead of re-running them.
`magus affected ci` computes which projects a diff reaches and runs the `ci`
target on those, against the merge base on a pull request and against the
previous commit on main (`docs/concepts/targets/ci.md`, "The merge is the first
time a step runs"). `magus affected ci --plan` splits that set into shards for
a job matrix, which is how `.github/workflows/ci.yaml` runs here. Pull requests
replay the shared cache and cannot write to it; a push to main is what
publishes, under a signed trust set (`magus.yaml`, `cache.remote.trusted_keys`;
the wiring is the `cache.remote` block in `magusfile.buzz`).

Worktrees are the ordinary workflow. `git worktree list` in this repository
prints 192 lines right now, 164 of them under `.claude/worktrees`, cut by
agents, and the machine budget
the daemon owns is what stops twelve of them from each admitting a full
machine's worth of work (`docs/guides/integrations/daemon.md`). The guard denies
a `cd` from one checkout into a sibling, because that tree's binary and cache
describe that tree and its verdict would describe neither
(`docs/guides/integrations/agents/guard.md`).

## Where this repository falls short

If the claim is "verdicts are the scarce resource and we made them cheap," a
reader should be able to check it, so here is today, tallied from this
checkout's run journals.[^journals]

The gate finished 21 times. Two runs were green, at 25 seconds and 6 seconds.
Nineteen were red, with a median wall time of 216 seconds and a longest of 490.
Red is the drift gate doing its job on a tree that kept changing under it, and
it is also more than an hour of machine time spent learning that.

Across every target today, 1,260 executions took about six and a half hours of
target time and 358 replays took 18 seconds. Replays were 22 percent of results.
The root `ci` target ran 19 times and replayed zero, by declaration: it composes
the gates and reads nothing itself, so its key is the whole tree, and the
members it composes through `ctx.needs` run inline and never mint a cache step
of their own. A shard that dispatches only `ci` publishes nothing to the remote
cache (`magusfile.buzz`, the `ci` policy comment). `go-build` ran 86 times,
replayed zero, and spent 22 minutes, because it declares no outputs on purpose.
`go-test:rw` ran 71 times, replayed zero, and spent 72 minutes, under 55
distinct commands, mostly `-run` filters.

That last one is the one I would have gotten wrong yesterday. The cache-yield
diagnostic, MGS1009, reports a target that executes repeatedly and never
replays, on the theory that its declared footprint is wider than what it reads.
It accused `go-test` of exactly that: 43 runs, 0 replays. Twenty of those runs
were twenty different `-run` filters, and a different command is supposed to
miss. The tally now reads the journal's exec records and stays silent when the
commands differ (`internal/cache/yield.go`), and the comment beside that check
admits what the fix costs: a target whose arguments vary and whose footprint is
also wrong will not be reported until someone runs it the same way twice.

Two more. Six projects declare an index whose content is a function of the
whole knowledge graph, and `affected` does not select them for every change
that feeds it, so CI runs a bare fan-out gate before any shard starts
(`.github/workflows/ci.yaml`, the "Fail fast on uncommitted generated
output" step). And the savings
lens, the number that would say what the cache and the affected set actually
bought, is still listed as a debt on the doctrine page. The figures above are
that lens done by hand, which is why they took a script and not a command.

## The thing to replace

The ledger is fine. It is content-addressed, it merges in milliseconds, and it
already knows which subtrees two branches share. What was built for a slower
world is the pipeline that turns a tree into a verdict, and it is slow because
it keys on the wrong thing and re-runs what it could have replayed. That is a
build-tool problem. It is the problem this tool exists for, and the numbers
above are how far it has gotten on its own repository today: further than a
commit-keyed pipeline, and not as far as the pitch.

[^journals]: Every `magus run` and `magus affected` here writes one journal to
    `.magus/runs/<invocation>.jsonl`; a `result` record carries `project`,
    `target`, `status` (`cached`, `pass`, or `fail`) and `dur_ms`, an `exec`
    record carries the command text, and `started` and `finished` records bound
    the invocation. The figures are sums over the 347 journals dated
    2026-09-15 in this checkout, taken while a gate was still running, so
    the day's final numbers are larger. `magus doctor`'s cache-yield check
    reads the same records.
