### Added

- **`libs/safetyscissors/wake` records the bounds on work that keeps going across turns.**
  A person's `Terms` name a deadline, a wake count, an idle limit, a minimum gap and an
  optional spend bound. `Store.Start` opens a turn and `Store.RecordWake` reports it,
  through a caller's `Log`. The pure `Decide` answers
  whether another turn may start, or why the wake ended.
- **A write past the terms is refused with a typed error.** That covers a start inside
  the gap, a second open turn, a report for another turn and any write to an ended wake.
  Under a spend bound, an unknown model or a 0 rate is an `*UnpricedError`. Nothing in
  the package sleeps, schedules or starts a turn.
