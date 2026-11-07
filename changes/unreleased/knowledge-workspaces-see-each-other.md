### Added

- **`knowledge.workspaces` connects workspaces both ways.** Each entry is
  `{name, path}` in the user config. Every load in a listed workspace makes
  `.magus/workspaces/<name>`, a symlink to each other entry, in both trees, and prints one
  line per change. Deleting an entry removes both symlinks on the next load.
  `magus describe workspace` lists the entries.
- **A target reads another workspace through its symlink.** `ctx.readsFiles` and Buzz
  imports take `.magus/workspaces/<name>/<path>`. The key names that path and the bytes,
  never the other root, and each other root is read-only in the sandbox.
- **The Go spell builds and runs another workspace's golangci-lint.**
  `spells/golang/golangci.buzz` builds a custom binary from a `.custom-gcl.yml` and runs it
  with a config, both read through the symlink.
- **Refusals for each way the list can be wrong.** MGS1054 (an entry names no workspace),
  MGS1055 (the list in a repository), MGS1056 (an entry's shape), MGS1057
  (`.magus/workspaces` not ignored), MGS1058 (a non-symlink in the way) and MGS1059 (a read
  through a workspace not connected here).
