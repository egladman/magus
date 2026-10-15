### Changed

- **The spawn rules compare model tiers by rank.** `spawn-names-model` at `strict` refuses a spawn whose model is not at the tier its job row was forked at, resolving the model through each harness's aliases in your model-tier declaration, and names the row's tier, rank, alias, source, reason and the declaration's line.
