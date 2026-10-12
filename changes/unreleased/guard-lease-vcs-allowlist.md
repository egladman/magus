### Changed

- **Breaking: a worker lease runs only the version-control verbs it is allowed.**
  `lease-vcs` refused a named list of verbs, so `git branch -f`, `git update-ref`,
  `git pull` or `hg strip` ran unjudged. A worker now runs reads, add, switch, branch
  creation, fetch and its own commit; every other verb, a config alias included, is
  refused.
