### Security

- **The agent guard closes four ways around its rules.** A shell line that opens with
  `*** Begin Patch` is judged as the shell line it is: only a tool its row lists under
  `writes` carries a patch. A push behind a wrapper (`env`, `exec`, `command`, `time`,
  `nohup`, `sudo`, `timeout`, `nice`, `find -exec`) keeps its arguments, so `push-ungated`
  reads the branch it publishes. `hook-wiring-write` judges a shell line's writes too, and
  covers the workspace's `./magus`: a subagent or a leased worker that redirects, copies,
  moves, links or removes `.claude/settings*.json` or the guard binary is refused, as a
  write tool already was for the settings. A person's own session keeps its allowance.
  A client script named by `path` is read against the workspace root and only inside it,
  as the daemon reads it, instead of against the hook's working directory.
- The branch-name advice quotes a branch name the shell would expand.
