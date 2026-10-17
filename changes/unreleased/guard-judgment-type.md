### Changed

- **The guard's judgment and the `next` breadcrumb are `types.GuardJudgment` and `types.NextStep`.** They moved
  out of `internal/guard` and `internal/hint` so a public method can return them. No field, JSON key or YAML key
  changed, so the hook verdict JSON and the `next` field of every structured result read as before.
