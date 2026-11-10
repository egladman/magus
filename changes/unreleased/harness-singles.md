### Changed

- **Claude Code's `EnterWorktree` and `ExitWorktree` have tool rows.** A call that names no command but a tree's `path`, or a new tree's `name`, from a tool the rows say runs, moves the session there. The guard lets it reach only a checkout a job gives the caller, and the `sibling-checkout` rule refuses any other, naming the checkouts the caller may enter.
- **The Buzz edit hook checks a file in the mode the static-check lint uses.** Files under `docs/`, `std/examples/`, `cmd/`, `console/` and `internal/queue/` check with `--embedded`, as files under `magusfiles/`, `spells/` and `hack/` already did, and a file under `docs/` checks from `docs/`.
