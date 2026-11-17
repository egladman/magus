### Changed

- **Breaking: `Store.Witness` runs the check itself.** `(*Store).Witness(ctx, id, goal, cmd)`
  now starts and waits on `cmd` and returns the `RunRef`; the `Witness` type and
  `(*Witness).Close` are gone. A caller still builds `cmd` and may set its Stdout,
  Stderr, Stdin, Cancel and WaitDelay. The exit status is the wait of the process the
  store started, so another command's `Process` or `ProcessState`, or a redirect swapped
  and restored, can no longer forge a run. `cmd.Path` must be the program `argv[0]`
  resolves to, and `Env`, `SysProcAttr` and `ExtraFiles` must be unset. The refusals
  `never-waited`, `other-process`, `redirected` and `closed` are gone; `other-program`,
  `env`, `sys-proc-attr`, `extra-files`, `not-started` and `unfinished` are new, and
  `WitnessRefusedError` gains `Err`.
- **Breaking: the job store's reads take a context.** `Store.Read`, `List`, `Unswept`,
  `Attempts` and `Grants` take `ctx`, which bounds the wait for an append in flight and
  for the lock a migration takes, and reaches the sweep. `Reader().List` passes its ctx
  on. `Store.LiveGrant` is removed; read `Grants` and `Attempts` and ask `Grant.Lowers`.
- **Breaking: `job.Grading` is renamed `job.WriteKind`.**
- **Breaking: a store needs `WithActor` and `WithNotices` to write.** A row write or a
  grant on a store opened without either is refused with an `*UnconfiguredError`
  naming the option. `Store.As` stands in for `WithActor`. A release digested with no
  `WithRoot` or no `WithDeclarationDigest` says so in a `Notice`.
- **Breaking: a stored record holding a member this build does not declare below a
  row's top is refused.** `jobs.jsonl` keeps unknown members on the row, its spec and
  its status (`Job.Unknown`); any nested record, and every level of an `attempts.jsonl`
  line, now refuses one with an `*UnknownMemberError` rather than drop it on the next
  write.
- **Breaking: `Store.Rewrite` refuses two rows under one id** with a
  `*DuplicateRowError`, and writes nothing.
- **The lock file beside `jobs.jsonl` names the holder's pid and when it took the lock**,
  not its command line. A lock file an older writer left names no holder.
- **`grants.jsonl` is written 0600.**
- **The store's clock is `WithClock` throughout:** a row's write time, a grant, an
  archive's name and the lock holder's time read it.
- **`magus job fork` and `magus job apply` say when a check goal's accept defaulted.** A
  check goal naming no `accept` still accepts `[witnessed]`, and each one prints a note.
  `job.DecodeStdinDeclaration` and `job.DecodeDeclarations` return the
  `[]job.DefaultedAccept` they resolved.
