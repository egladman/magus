### Fixed

- **A Buzz fs write to a computed path is judged at the write.** `fs\writeFile`,
  `writeFileAtomic`, `writeLines`, `appendFile`, `copyFile` and `copyDir` grade a rewrite of
  an existing workspace file the way `refs --rename` grades its edits: a declared output is
  refused, and the lease, claim and policy verdicts are the Edit tool's. A target run still
  writes its own declared outputs. A lease store that does not read refuses the edit.
