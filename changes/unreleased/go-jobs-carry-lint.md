### Changed

- **A job that writes Go source carries `lint`.** A fork or apply whose write paths include
  Go source and whose check and postconditions do not run `lint` adds a `lint` postcondition with
  `no_default_charms` and prints a note saying so. A `**` deviation of MGS3045 no longer
  permits the lint gate; only its exact subject does.
