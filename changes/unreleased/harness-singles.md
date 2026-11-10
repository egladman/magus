### Changed

- **Claude Code's `EnterWorktree` and `ExitWorktree` have tool rows.** A call that names no command but a tree's `path`, or a new tree's `name`, from a tool the rows say runs, moves the session there. The guard lets it reach only a checkout a job gives the caller, and the `sibling-checkout` rule refuses any other, naming the checkouts the caller may enter.
