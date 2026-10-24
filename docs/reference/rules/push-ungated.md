---
title: "push-ungated: a push or `gh pr create` at a commit with no green gate; refused for every session"
description: "A deny rule by default: it refuses a push or `gh pr create` at a commit with no green gate; refused for every session, and names what to run instead."
tags: [guard, rules, push-ungated, deny]
---

# push-ungated

A deny rule by default: it refuses a push or `gh pr create` at a commit with no green gate; refused for every session, and names what to run instead.

## What it catches

A push or `gh pr create` at a commit with no green gate; refused for every session.

## Why

The advisory this replaced fired on EVERY push, having read nothing: it told a caller who had just gated and a caller who had never gated the same sentence, which is a toll rather than a reminder. This one reads the run log, so the finding is a fact: which invocations ran the gate, which commit each was built from, and how it finished. That is what makes stopping the call legitimate here where the rest of this tier only advises. It matches on the COMMIT and not the exact tree, deliberately: an exact match would expire on the first comment typo after a green run, which is the delta the cadence already says to push, and a rule that fires there is one people route around. A session no job lease binds is the orchestrator. It used to be asked, and two pull requests went out with no local gate because the rule only asked, so it is now refused with the gate named: `magus status --wait`, then `magus affected ci`. Opening a pull request with `gh pr create` meets the same gate at the head commit. A session bound to a lease is a worker, and workers do not publish: its refusal also sends the commit to the orchestrator. A push that follows a commit, merge, cherry-pick, am, rebase, reset or pull on the same line (git, hg, sl or jj; joined by `;`, `&&`, `||`, a newline or a subshell) is denied for everyone: the revision is read before the line runs, so a gate recorded at that HEAD says nothing about the commit that goes out. Run the commit alone and push in the next call, where the push is graded at the real HEAD. A person who skips the gate on purpose types `magus gate waive <commit> --reason "..."`: a push or `gh pr create` of that commit, and of no other, then passes, and the verdict names who waived it, when and why. A revoked waiver covers nothing.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"push-ungated": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [push-ungated]: ...
```

A workspace that sets this rule to `advise` sees `advise [push-ungated]: ...` instead.

`magus describe rule push-ungated` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
