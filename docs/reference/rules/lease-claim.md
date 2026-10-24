---
title: "lease-claim: a leased write outside its write paths that magus proves belongs to the job"
description: "A deny rule by default: it refuses a leased write outside its write paths that magus proves belongs to the job, and names what to run instead."
tags: [guard, rules, lease-claim, deny]
---

# lease-claim

A deny rule by default: it refuses a leased write outside its write paths that magus proves belongs to the job, and names what to run instead.

## What it catches

A leased write outside its write paths that magus proves belongs to the job.

## Why

A write outside the write paths is still refused: the boundary is the declaration, and the guard never changes it inside a hook. When magus can prove the path belongs to the job, the refusal names the proof and serves the one `magus job apply` line that claims the path for the job, instead of sending the worker to its orchestrator. A proof is one of four: refs:<symbol>, a Go file referencing a declaration the job's diff changed or removed inside its write paths; test:<file>, a test beside a file the job changed (a _test file in its directory, or a file under its testdata) that names what the file defines; output:<project>:<target>, a declared output of a target whose sources the job changed; and released, a path this attempt's holder released. The store proves the record again under its lock when it applies, and refuses a path another live job holds (MGS3032), a deny path, a path outside the checkout, and any claim past the job's max_claims (3 when unset), which jobs.max_claims in magus.yaml (default 10) caps. A write no proof covers stays lease-write. Every message is rendered by magus from the row and the tree; no model is asked.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"lease-claim": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [lease-claim]: ...
```

A workspace that sets this rule to `advise` sees `advise [lease-claim]: ...` instead.

`magus describe rule lease-claim` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
