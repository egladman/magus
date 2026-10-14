### Changed

- **The model table comes from your harness spells, not from the binary.** A harness spell
  returns its models, with context windows and prices, from the new `harness_models` op;
  `claude-code` ships the Claude rows. The guard prices and sizes a session from the rows of
  the harnesses your magusfile wires, then `agents.models` in `magus.yaml`, which now also
  takes `input`, `output`, `cache_write_5m` and `cache_write_1h`. A workspace that wires no
  harness and sets no `agents.models` still loads; the cost advice then has no facts about a
  model, as for any model the table does not know. `magus doctor` no longer reports the table
  as stale, since a spell's rows are as current as the spell, and a rule's `tokens` is no
  longer checked against a built-in largest window.
