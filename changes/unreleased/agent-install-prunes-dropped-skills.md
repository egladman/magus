### Changed

- **`magus agent install` removes the skills the catalog dropped.** A skill directory that
  carries magus's stamp but is no longer shipped is deleted after the install writes, and
  each removal is printed on stdout. A skill without the stamp is never touched, so a
  hand-authored skill beside the installed ones survives. `--dry-run` lists what would go.
  The `--prune` flag is removed: pruning is the default, as it already was for
  `magus agent harness install`, which now prints its removals on stdout in the same words.
- **`magus doctor` names a leftover skill directory it cannot prune.** An empty directory,
  or one whose `SKILL.md` cannot be read, is reported as stale with the `rmdir` or `rm -r`
  that removes it, instead of "cannot read it". A reinstall now clears a dropped skill, so
  the finding no longer says a reinstall alone will not fix it, and `--fix` runs the
  reinstall.
