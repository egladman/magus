### Changed

- The job store is refused by its schema, not by the commit that wrote it. A worker on an older base can file its own result after a newer magus wrote the store, and MGS3051 now names the feature the store requires.
