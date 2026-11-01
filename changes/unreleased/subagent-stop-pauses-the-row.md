### Changed

- **A subagent's stop no longer ends its job.** The stop stamps the row `holder_stopped`,
  the binding stays live, and the holder's next hook call clears the stamp, so a continued
  worker carries on with no `magus job exec`. A stop 30 minutes old counts as a dead holder.
  The guard names no host's message tool.
