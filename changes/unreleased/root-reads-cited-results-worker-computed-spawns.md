### Changed

- **orchestrator-groundwork lets the root read a file a filed job result names** in its
  changed_paths, unresolved_risks or follow_ups.
- **interpreter-script refuses a leased worker's computed proc spawn.** An inline Buzz
  program, or a Buzz script outside the workspace, whose proc\exec or proc\shell argv is
  computed at run time is refused for a worker; repository scripts pass.
