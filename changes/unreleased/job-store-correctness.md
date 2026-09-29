### Fixed

- **The job store judges a job by what it declares now.** A declared job queued on a
  live dependency is no longer ended as stale; its clock starts when the dependency
  ends. A re-fork is ordered by its new depends_on, a put that adds a directory write
  path is refused (MGS3018), and an abbreviated checkpoint no longer reads as a
  diverged base.
- **The guard's "installed skill" advice no longer fires on a skill's source.** It reads
  the `source: magus` stamp from the frontmatter only, so editing
  `internal/agent/skills/*/SKILL.md` is not mistaken for editing an installed copy.
