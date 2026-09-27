### Changed

- **The guard denies pushes, merges, spawns and shared-state writes while its policy cannot
  load.** When neither the working tree nor its approved copy loads (a `./magus` older
  than the tree, say) and the policy registered a command or spawn rule the last time it
  loaded, `git push`, `gh pr merge`, a subagent spawn and the state-writing magus verbs are
  denied with the rebuild that fixes it. Every other call passes with the notice, which
  now also rides on deny and ask verdicts. A rule that loads and then raises still fails
  open.
