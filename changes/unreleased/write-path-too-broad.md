### Added

- **A job record whose write path claims more than the work refuses as MGS3043.** A glob
  matching more than one project or 50 files, or starting with `**` at the root, is too
  broad. The fix lists the narrowest paths the job's goals name and the deviation that keeps
  a sweep. Literal paths and read-only jobs are never measured.
