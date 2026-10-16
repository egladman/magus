### Changed

- **The outputs a job's target regenerates outside its write paths, and the ci targets it does not run, are problems of the record, not notes.** A fork or apply that would have printed them now refuses with MGS3044 or MGS3045 unless the record adds the output or goal, or waives it in `waivers`.
