### Changed

- **Breaking: `Store.Witness` runs the check itself.** `(*Store).Witness(ctx, id, goal, cmd)`
  starts and waits on `cmd` and returns the `RunRef`; the `Witness` type is gone. The exit
  status is the store's own wait, so no other process can forge a run.
- **Breaking: a witnessed `cmd` is held to what it runs.** `cmd.Path` must be what
  `argv[0]` resolves to, and `Env`, `SysProcAttr` and `ExtraFiles` stay unset. The
  refusals `other-program`, `env`, `sys-proc-attr`, `extra-files`, `not-started` and
  `unfinished` are new.
- **Breaking: the job store's reads take a context.** `Read`, `List`, `Unswept`,
  `Attempts` and `Grants` take `ctx`. `Store.LiveGrant` is removed; read `Grants` and
  `Attempts` and ask `Grant.Lowers`.
- **Breaking: `job.Grading` is `job.WriteKind`.**
- **Breaking: a store needs `WithActor` and `WithNotices` to write.** A write without
  either is an `*UnconfiguredError` naming the option; `Store.As` stands in for
  `WithActor`.
- **Breaking: a nested stored record with an undeclared member is refused.** Rows keep
  unknown members at their top; deeper ones, and any in `attempts.jsonl`, are an
  `*UnknownMemberError`.
- **Breaking: `Store.Rewrite` refuses two rows under one id** with a `*DuplicateRowError`.
- **The job store's lock file names the holder's pid and when it took the lock.**
- **`grants.jsonl` is written 0600.**
- **The store's clock is `WithClock` throughout.** Row times, grants, archive names and the
  lock holder's time read it.
- **`magus job fork` and `magus job apply` note a check goal's defaulted accept.** One
  naming no `accept` still accepts `[witnessed]`.
