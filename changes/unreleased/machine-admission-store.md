### Changed

- **`types.MachineRefusal` is now `types.MachineAdmission`.** It gains `outcome`,
  `under`, `waited_ms`, `wait_bound_ms`, `sizing`, `budget_mb`, `held_mb`,
  `budget_slots` and `held_slots`, so `refused[]` in `magus server status -o json`
  carries them. `MachineClaim` gains `under`, `slots_floor`, `wait_bound_ms` and
  `waited_ms`, and `MachineVerdict` gains `slots` and `ahead`; the broker protocol
  stays 5. `cache.FormatMB` moved to `types.FormatMB`.
