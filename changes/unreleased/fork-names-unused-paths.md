### Changed

- **A fork refused over another job's file names what that job has not used.** The
  MGS3032 and MGS3046 refusals name the holder, its last write, and the paths its diff has
  not touched, with the `magus job apply -f <record>` that narrows it and the
  `--depends-on` that waits. Each refusal files a release request; a dry run files none.
