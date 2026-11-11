### Fixed

- **interpreter-rewrite and conflict-rewrite place a relative path from the call's
  directory.** A script run from a subdirectory was judged as if it ran at the root, so
  it could rewrite a tracked file unrefused, or be refused for a file it would create.
- **check-timeout prices a narrowed run by its own last run.** A `magus run ... -- -run X`
  with a recorded run of the same arguments is priced by that run; one with none is
  priced by the whole target, and the advice says which.
- **A skill-gate deny names the installed SKILL.md.** An agent with no Skill tool can
  clear architecture-before-skill, spawn-before-skill and buzz-before-skill by reading that file,
  which the guard already counted as a load.
