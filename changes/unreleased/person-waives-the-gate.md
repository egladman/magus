### Added

- **`magus gate waive <commit> --reason "..."` lets a person skip the local gate for one
  commit.** push-ungated then lets a push or `gh pr create` of that commit through, and no
  other; the waiver records who typed it, when and why, and `--revoke` removes it. The
  guard refuses the verb to every agent under the new rule `waiver-is-a-persons`.
