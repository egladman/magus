### Added

- **A job record whose write path claims far more than the work refuses as MGS3043.** A
  glob write path that matches files in more than one project, more than 50 files, or
  starts with `**` at the workspace root is too broad. The fix lists the narrowest write
  paths the job's symbol goals and criteria name, and the waiver that keeps the path when a
  sweep is the job: `{"code":"MGS3043","subject":"<path>","reason":"..."}`. A
  `file#declaration` claim, a literal path and a read-only job are never measured.
