### Added

- **Breaking: the merge queue labels `merge-queue: changes a generator` on a queued pull
  request whose generated files it cannot regenerate itself.** The author learns before
  any kick-back that main moving them means merging main in and regenerating. A provider
  script must export `flag`; label creation GitHub refuses as invalid is now an error.
