### Changed

- **A scheduled prompt is refused as a timer while background work is pending.** Claude
  Code's `ScheduleWakeup` is no longer an inert tool. While the session has a running
  background task or a child that has not stopped, the guard denies it under `sleep-poll`
  and points at the notice that work already sends. With nothing pending it passes.
