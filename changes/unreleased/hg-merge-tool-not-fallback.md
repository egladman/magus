### Fixed

- **Mercurial no longer sends every conflicted file to the magus merge tool.** With no
  `ui.merge` set, Mercurial falls back to any registered merge tool, so the magus tool
  took hand-written files too and left them conflicted. magus now registers the tool
  with `disabled = True`, which only that fallback reads, so `[merge-patterns]` alone
  routes the declared outputs to it. Sapling reads both keys the same way. A hand file an
  output's exclusion carves out is named first in `[merge-patterns]` with Mercurial's
  own `:merge`.
