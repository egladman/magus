### Changed

- **`proofread-reply.buzz` says when it judged nothing.** A proofread that is missing,
  stale, exits 2 or prints unreadable JSON gets one notice per session naming the binary
  and its fix. In a checkout of magus it judges with the proofread that checkout builds,
  checked for staleness by its own proofread module. The reply is never blocked.
