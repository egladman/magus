### Fixed

- **A derived lint postcondition skips files the VCS ignores.** A write glob such as
  `.agents/skills/**` also matched installed, gitignored skill files, and markdownlint
  failed a job on files nobody wrote. The job notice says how many files were left out,
  or that the VCS could not be asked and every file was kept.
