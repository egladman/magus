### Added

- **A failed composed target's output names the `ctx.needs` member that failed.** The
  descriptor records `failed_need_target` and `failed_need_ref`, and `magus query output <ref>`
  (text, `--identity`, `--stdin`) and the unchanged-inputs hint print `magus query output`
  for the member's ref, where the findings are. The error text is unchanged.
