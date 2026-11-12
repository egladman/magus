### Changed

- **Breaking: a job's model, criteria and fresh_reason move into one `agent`
  section.** It holds `model {id, tier}`, `instructions` and `worker {type, reason}`,
  where `type` is always `resume` or `new`. `job fork --criteria` is `--instructions`,
  and `--new-worker <reason>` skips the warm worker. `jobs.jsonl` migrates once.
