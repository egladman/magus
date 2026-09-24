### Fixed

- **`magus bisect` names the culprit on current git.** Newer git writes
  `# first 'bad' commit:` in the bisect log, which the parser did not read.
