### Fixed

- **A background `magus-scout` can read the workspace.** The guard recorded a child's
  agent type only when it joined a pending spawn, so a host whose finished spawn names the
  child first left the type unknown and denied every Read and text Bash as an unbriefed
  structure probe. Both paths now record it.
