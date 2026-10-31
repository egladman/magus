### Changed

- **The agent guard fails closed when it cannot judge.** With no magus found, or one that
  cannot answer, the shipped hooks refuse every call that may change state and let a read
  through. Every such call tells the person through the host's notice channel how to
  install or build magus, and the agent reads the same fix. `magus-command.buzz`,
  `magus-path.buzz`, `cursor-hook.buzz` and `opencode-plugin.ts` all do this. Cursor and
  OpenCode refuse reads too, since a refusal is the only reply the person sees there. A
  Stop, SubagentStop or PermissionRequest notice is a `systemMessage` now, not stderr.
- **A guard that falls back from the checkout's `./magus` says so to the person.** A
  `./magus` that cannot run, or a checkout of magus with none, is named in every refusal,
  and once per session as a `systemMessage` on a call PATH's magus judged.
- **`magus-buzz-check.buzz` blocks when it cannot check.** No runnable magus, or a check
  that raised, is a block reason naming the binary and why.
- **`proofread-reply.buzz` says when it judged nothing.** A proofread that is missing,
  stale, exits 2 or prints unreadable JSON gets one notice per session naming the binary
  and its fix. In a checkout of magus it judges with the proofread that checkout builds,
  checked for staleness by its own proofread module. The reply is never blocked.
- **Recorder hooks say when they recorded nothing.** The checkpoint, observe, rehydrate
  and reclaim-disk hooks, and the message record in `magus-command.buzz`, give one notice
  per session when no magus runs. When no magus can name the workspace, the session-load
  adapters say why and scope to the `magus.yaml` above them.
