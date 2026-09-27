### Changed

- **The guard denies pushes, merges, spawns and shared-state writes while its policy cannot
  load.** When neither the working tree nor its approved copy loads, and the policy last
  registered a command or spawn rule, those calls are denied with the rebuild that fixes
  it. Every other call passes with the notice.
