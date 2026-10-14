---
title: Model tiers
description: Declare the model tiers your jobs run at in the root magusfile, map each to a harness's model, and optionally register a function that picks a tier when a job is forked.
tags:
  [
    jobs,
    magus job,
    model tiers,
    models,
    skills,
    magus job fork,
    MGS1052,
  ]
---

# Model tiers

A job row can say which model its worker runs on. A model tier names a rung of
that choice in your own words, cheapest first, so a row, a rule or a report says
`economy` instead of a model id that changes every few months. The magusfile
declares the tiers once. magus records the tier on each job when it is forked and
never changes it afterward.

magus does not route anything. It never picks a model for a call, retries a job
at a dearer tier or switches a worker mid-run. A tier changes only when someone
edits the row, the matrix function or the declaration.

## What a workspace gets by default

With no declaration, the workspace uses the `magus/modeltiers` module: `economy`,
`standard` and `principal`, with `standard` as the default. Economy jobs are
capped at four live at once and 100000 tokens of context, and economy workers
load the `full` skill bodies, since a smaller model is the reader least able to
re-derive the steps the `short` form leaves out. The module names no model. Each
harness maps the tiers to its own models.

`magus\job.modelTiers(null)` turns tiers off.

## Declare your own

Copy the module into the root magusfile and change what you need:

```buzz
import "magus";

enum ModelTier { economy, standard, principal }

fun claudeCode(tier: ModelTier) > str {
    return match (tier) {
        .economy -> "haiku",
        .standard -> "sonnet",
        .principal -> "opus",
    };
}

fun bounds(tier: ModelTier) > magus\ModelTierBounds {
    return match (tier) {
        .economy -> magus\ModelTierBounds{ maxLive = 4, maxContext = 100000 },
        .standard -> magus\ModelTierBounds{ maxLive = 2 },
        .principal -> magus\ModelTierBounds{ maxLive = 1 },
    };
}

fun skills(tier: ModelTier) > magus\SkillVariant {
    return match (tier) {
        .economy -> magus\SkillVariant.full,
        .standard -> magus\SkillVariant.short,
        .principal -> magus\SkillVariant.short,
    };
}

magus\job.modelTiers(ModelTier, default: ModelTier.standard,
    hosts: {magus\HarnessKey{ id = "claude-code" }: claudeCode},
    bounds: bounds, skills: skills);
```

- **The enum is yours.** Its order is the rank, cheapest first. A typo such as
  `.standrd` fails `magus check`, like any other enum.
- **Every mapping is a `match` over the enum.** Add a tier and `magus check`
  fails until `hosts`, `bounds` and `skills` each cover it.
- **`hosts` is keyed by harness.** The key is a `magus\HarnessKey` whose id is
  the harness spell's name, and the harness must already be wired with
  `magus\harness.provider` above the call. The function answers the model alias
  that harness spawns for the tier, such as the string `sonnet`. Two tiers cannot
  share an alias on one harness, because a spawn of that alias would name no one
  tier.
- **`bounds` caps a tier.** `maxLive` is how many jobs at the tier may be live at
  once and `maxContext` the tokens a worker at the tier may reach. A field left
  out is no cap.
- **`skills` picks the skill variant** the tier's workers load: `short` or
  `full`.

magus calls each function once per tier while the magusfile loads, and keeps
none of them. A declaration it cannot use stops the load with
[MGS1052](../../reference/codes/magusfile/MGS1052.md): tiers that are not an enum,
a default that is not one of its cases, a host the magusfile did not wire, a
function that raises or answers the wrong type, a second call, or a call from a
project's magusfile instead of the root one.

## Which tier a job gets

A fork assigns the tier in this order, and records which step decided:

1. **The row.** A tier or model the row names wins (`row`).
2. **The matrix.** The function registered with `magus\job.modelTier`, when the
   row names neither (`matrix`).
3. **The default.** The declaration's default (`default`).

`magus job describe` prints the tier, the step that chose it and the reason.

## Pick a tier with a matrix

The matrix is a function of the job's declared record and the workspace's
recorded history. It runs once, at fork, with the job store read-only: every
`magus\job` member raises inside it. It returns `null` to let the default decide,
or an object with `tier`, `skills` and a non-empty `reason`. A result missing any
of the three fails the fork.

```buzz
import "magus";
import "magus/modeltiers" as modeltiers;

object Choice { tier: modeltiers\ModelTier, skills: magus\SkillVariant, reason: str }

fun economyHolds(req: ModelTierRequest) > bool {
    foreach (r in req.history) {
        if (r.rank == 0 and r.kind == req.kind and r.jobs >= 10 and r.passLower < 0.5) { return false; }
    }
    return true;
}

fun choose(req: ModelTierRequest) > Choice? {
    if (req.readOnly or req.check == null) { return null; }
    if (req.writeFiles <= 3 and req.writeProjects.len() == 1 and economyHolds(req)) {
        return Choice{
            tier = modeltiers\ModelTier.economy,
            skills = magus\SkillVariant.full,
            reason = "{req.kind.value} job, {req.writeFiles} files",
        };
    }
    return Choice{
        tier = modeltiers\ModelTier.standard,
        skills = magus\SkillVariant.short,
        reason = "wide write set, or economy failing {req.kind.value}",
    };
}

magus\job.modelTier(choose);
```

This one sends small, checked jobs to economy until economy has failed half of
ten or more jobs of the same kind. The history is the same record
`magus insight` reports, so the matrix and a person read one set of numbers.
The same file is the compiled example under `std/examples/magus`.

Read-only and unchecked jobs fall through to the default, because a job nothing
can verify is the wrong place to try a cheaper model.

## Where to go next

- [Jobs](../jobs.md): the plan a tier is recorded on.
- [`magus\job.modelTiers`](../../reference/buzz/magus.md) and `magus\job.modelTier`
  in the Buzz reference.
