### Changed

- **A run compiles an imported spell once per importing directory.** Its later loads reuse
  the compile while the spell's path, bytes and importing directory match, so a spell edited
  mid-run compiles again. A compile cut short by a deadline is no longer remembered as "no
  spell here". The embedded declarations parse through the shared cache.
