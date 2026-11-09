### Changed

- **An integrate row merges any spelling of a branch it lists.** `<b>`, `origin/<b>`,
  `refs/heads/<b>` and `refs/remotes/origin/<b>` pass, with `--no-ff`, `--no-edit` or `-m`.
  A session holding a parentless integrate row may fork sibling rows.
