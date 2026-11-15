### Changed

- **Breaking: stores no longer read the integer `schema_version` stamp.** The job store,
  `query output --stdin` records, the owed-regeneration record, the knowledge manifest and
  the tool rows refuse a record an older magus stamped with an integer, naming it as
  carrying no `apiVersion`. Move the old store aside or write the record again with this
  magus.
- **A `magus.lock` from a newer magus is refused for its version.** It used to be
  refused for its first unknown field.
