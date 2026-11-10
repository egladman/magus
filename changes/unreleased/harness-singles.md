### Changed

- **Claude Code's `EnterWorktree` and `ExitWorktree` have tool rows.** A call from a tool that runs, carrying no command but a tree's `path` or a new tree's `name`, moves the session there. The guard lets it reach only a checkout a job gives the caller; `sibling-checkout` refuses any other and names the checkouts it may enter.
- **The Buzz edit hook checks a file in the mode the static-check lint uses.** Files under `docs/`, `std/examples/`, `cmd/`, `console/` and `internal/queue/` check with `--embedded`, as files under `magusfiles/`, `spells/` and `hack/` already did, and a file under `docs/` checks from `docs/`.
