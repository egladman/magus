### Changed

- **Breaking: the reply check is on no host until you wire it.** The Claude Code, Codex and Cursor spells no longer print a `Stop` entry for `proofread-reply.buzz`, `codex-hooks.json` drops it, and the OpenCode plugin runs it only when `__PROOFREAD_REPLY_BUZZ` names a copy. Entries added by hand or by an earlier merge stay; each host page shows the entry to add.
