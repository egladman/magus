### Fixed

- **An aliased import's types are reachable only through its alias.** After
  `import "m" as m;`, a bare `Run` named m's `Run` to the checker but the host's
  `Run` at run time. Now a bare name means the importer's own or a flat import's
  type in both, and m's is `m\Run`, as in upstream Buzz.
