### Changed

- **Breaking: a fork refuses a row that writes a package's code and its existing tests.**
  When a row's write paths cover a directory's code and test declarations that exist at
  its base, the fork reports MGS3060. Its fix prints two rows: the row itself, denying each
  existing test declaration, and a row a new worker takes for them after it. New test files
  and new test declarations stay allowed beside the code.
- **A broad write path's fix no longer suggests the tests beside a symbol's file.** The
  narrowest cover MGS3043 prints names the defining files and the criteria's files only.
