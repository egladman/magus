### Changed

- **Breaking: the reply check is on no host until you wire it.** The Claude Code, Codex
  and Cursor harness spells no longer print a `Stop` entry for `proofread-reply.buzz`,
  the shipped `codex-hooks.json` drops it, and the OpenCode plugin runs it only when
  `__PROOFREAD_REPLY_BUZZ` names a copy of the script. A merge never counts the entry as
  magus's, so one you add by hand stays and one an earlier merge wrote stays until you
  remove it. Each host page shows the entry to add.
