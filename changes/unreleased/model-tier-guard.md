### Changed

- **The spawn rules compare model tiers by rank.** `spawn-names-model` at `strict` refuses a
  spawn whose model is not at the tier its job row was forked at, resolving the model through
  each harness's aliases in your model-tier declaration, and names the row's tier, rank,
  alias, source, reason and the declaration's line. A harness the declaration maps no model
  for is refused, naming the `hosts` entry to add. A row forked with no tier is judged only on
  whether the spawn names a model.
- **`spawn-principal-for-economy` is now `spawn-above-model-tier`**, and advises on any spawn
  whose model ranks above its row's tier, by rank and never by price or name. It still never
  refuses. A `magus\guard.builtins` entry naming the old rule fails to load; rename it.
- **A `root-context-budget` `tokens` figure above every window of the model table is
  refused.** The guard reads the table from your harness spells and `agents.models`, so the
  check runs on the main thread's commands, where the table is known, and the refusal names
  the largest window.

### Added

- **`model-tier-context-cap` advises a leased worker past its tier's context cap.** When the
  context a worker's host reports passes the `maxContext` bound of the tier its row was
  forked at, its next write is told to report and stop. It advises by default at every
  level; set to deny in `magus\guard.builtins`, it refuses the worker's writes, as a lease
  past its deadline is refused.
- **A `magus\guard.spawn` rule reads the spawned job's model tier.** `SpawnRequest` carries
  `modelTier`, the tier recorded on the live job row the spawn's title names, or null.
