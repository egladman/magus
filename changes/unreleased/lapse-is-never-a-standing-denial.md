### Fixed

- **A lapsed policy read is filed as an unfinished guard check.** When the guard runs out
  of time reading a workspace's target policies, the deny is filed under `guard-unfinished`
  and told in full each time, never as a standing `human-only` refusal or a repeat.
