### Fixed

- **The guard lets a tree that cannot load put back the file its error names.** While a
  magusfile import names a missing file (BZZ2001), `git mv` onto that path, `git restore`
  or `git checkout --` of it, and an edit to it run with an advisory that says why, and
  the bootstrap command the deny prints runs too. Every other change stays denied.
