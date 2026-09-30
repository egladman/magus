### Fixed

- **Mercurial no longer sends every conflicted file to the magus merge tool.** With no
  `ui.merge` set, hand-written files fell back to it and stayed conflicted. The tool is
  now registered with `disabled = True`, so only `[merge-patterns]` routes declared
  outputs to it, in Sapling too. A hand file an output's exclusion carves out gets
  Mercurial's `:merge`.
