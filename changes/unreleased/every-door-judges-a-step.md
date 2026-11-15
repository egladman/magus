### Added

- **`magus shell` and the hook judge a `Step` record.** Send an `agent.Step` as JSON
  in place of a host's envelope. A step of kind `tool` is judged as the envelope it
  stands for. Any other kind goes to the step rows, and a record that cannot be judged
  is denied under `step-input`.
  [ADR 0046](https://eli.gladman.cc/magus/decisions/0046-the-judge-takes-any-agent-step/).
- **A `spawns` step row is judged by the spawn rules.** `Magus.Judge` judges its step as a
  host's spawn, through the built-in spawn rules and `magus\guard.spawn`.
  `spawn_description_field` is a JSON Pointer to the description.
