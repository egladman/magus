### Changed

- **`magus doctor` names a leftover skill directory install cannot prune.** An empty
  directory, or one whose `SKILL.md` cannot be read, is reported as stale with the `rmdir`
  or `rm -r` that removes it. A dropped stamped skill is now fixed by a reinstall, which
  `--fix` runs.
