### Security

- **The agent guard no longer runs compiled rules it read from the working tree.**
  Compiled guard rules are kept under `$XDG_CACHE_HOME/magus/buzz-bytecode`, keyed by
  build and workspace, instead of `.magus/`. The load of the approved (committed) rules
  uses no stored chunk. A new build removes chunk directories no build has used for a
  day.
