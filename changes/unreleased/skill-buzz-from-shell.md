### Added

- **A magus-buzz-from-shell skill for rewriting shell scripts as Buzz.** It maps each common
  shell idiom to the host module that does the job, pairs the usual shell with the Buzz it
  becomes, names the anti-patterns (`proc\exec` on a tool a module replaces, command lines
  built from strings, ignored results), and says when a script should become a target.
