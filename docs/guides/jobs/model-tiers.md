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
re-derive the steps the `short` form leaves out. The module names no model, and
the default maps no harness to one: until a magusfile maps a harness with
`hosts`, below, a tier has no model alias for any harness.

`magus\job.modelTiers(null)` turns tiers off.

## Declare your own

Copy the module into the root magusfile and change what you need:

```buzz
import "magus";
import "spells/harness/claude-code" as claudecode;

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

final cc = magus\harness.provider(claudecode);

magus\job.modelTiers(ModelTier, default: ModelTier.standard,
    hosts: {cc: claudeCode},
    bounds: bounds, skills: skills);
```

- **The enum is yours.** Its order is the rank, cheapest first. A typo such as
  `.standrd` fails `magus check`, like any other enum.
- **Every mapping is a `match` over the enum.** Add a tier and `magus check`
  fails until `hosts`, `bounds` and `skills` each cover it.
- **`hosts` is keyed by harness.** `magus\harness.provider` returns a
  `magus\HarnessKey`; keep it, as `cc` above, and use it as the key. A call that
  ignores the return value wires the harness as before. The harness must be wired
  above the declaration. The function answers the model alias that harness spawns
  for the tier, such as the string `sonnet`. Two tiers cannot share an alias on
  one harness, because a spawn of that alias would name no one tier.
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

The store assigns the tier when a row is created or its `model` changes, whichever
door writes it: `magus job fork`, `magus job apply`, or `magus\job.put` from a
magusfile or the client MCP tool. It tries these in order and records which step
decided:

1. **The row.** A `model` that is a tier name wins (`row`), and so does a `model`
   that a harness's `hosts` function maps to a tier, such as `opus`: the job takes
   that tier and keeps the model. An alias that two harnesses map to different
   tiers fails the fork, since it names no one tier. A `model` that names no tier
   and no alias is kept as the row's word and the job takes the declared default's
   rank; hosts name their models differently, so the fork records the mismatch in
   the reason instead of refusing it. `magus job fork --model` takes either.
2. **The matrix.** The function registered with `magus\job.modelTier`, when the
   row names no model (`matrix`). It may decline with `null`.
3. **The default.** The declaration's default, with the skill variant the
   declaration gives it (`default`). A workspace that declares nothing records the
   shipped default.

The record holds the tier, its rank, the skill variant, the source, the reason and,
for a matrix, the magusfile that registered it. `magus job fork` prints it:

```text
model tier: economy (rank 0), matrix: "1 file" at magusfile.buzz
```

`magus describe job` prints the same line and the skill variant under it, and
`magus ls jobs` shows the tier in the MODEL column of a row that names no model.
Editing a row without changing its `model` leaves the tier alone, and nothing
re-runs the matrix: a tier changes when someone edits the row, the matrix or the
declaration.

A fork that would put more jobs at a tier than its `maxLive` allows is refused
before anything is written, and the message names the bound. The count is the
live jobs recorded at that tier, read inside the write, so two forks racing for the
last place cannot both take it. A matrix that raises, or answers with a tier the
declaration lacks, a skill variant that is not one, or no reason, fails the fork;
it never falls through to the default.

`magus job apply --dry-run` assigns nothing and does not run the matrix.

## Pick a tier with a matrix

The matrix is a function of the job's declared record and the workspace's
recorded history. The record's `writeProjects` are the projects that own the
job's write paths, found the way `magus describe job` finds a job's projects.
`history` is empty today: the job outcomes it will read are not recorded yet.

It runs once, at fork, with the job store read-only: every `magus\job` member
raises inside it. It returns `null` to let the default decide, or an object with
`tier`, `skills` and a non-empty `reason`. A result missing any of the three fails
the fork.

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
ten or more jobs of the same kind. Once the history is recorded it is the same
set of numbers `magus insight` reports, so the matrix and a person read one record.
The same file is the compiled example under `std/examples/magus`.

Read-only and unchecked jobs fall through to the default, because a job nothing
can verify is the wrong place to try a cheaper model.

## Where to go next

- [Jobs](../jobs.md): the plan a tier is recorded on.
- [`magus\job.modelTiers`](../../reference/buzz/magus.md) and `magus\job.modelTier`
  in the Buzz reference.
