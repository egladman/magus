### Added

- **`magus job grant --commit <commit> push-ungated --reason "..."` lets a person skip the
  local gate for one commit.** push-ungated then lets a push or `gh pr create` of that commit
  through, and no other; the waiver records who typed it, when and why, and `--revoke`
  removes it. The guard refuses the verb to every agent under `grant-is-a-persons`.
