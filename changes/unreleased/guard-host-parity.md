### Fixed

- **Host guards judge native commands in the correct checkout.** Recognize `cmd`
  and `workdir`, give Codex an explicit interpreter handoff, and retain bounded
  stderr diagnostics when verification fails. Report legacy state conflicts
  without recommending removal of an active job plan. Hook template version 31
  identifies copies that need updating.
