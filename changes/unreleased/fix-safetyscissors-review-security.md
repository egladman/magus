### Security

- **A lease's literal write path grants at most its immediate parent.** A path under a
  missing directory grants nothing, where it granted the nearest existing ancestor,
  which could be a whole top-level directory.
