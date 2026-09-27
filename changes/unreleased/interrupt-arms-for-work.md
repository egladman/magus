### Changed

- **Ctrl+C asks twice only when it would discard work.** A read or a follow stream
  (`events -f`, `status -W`, `watch`) and a run between targets stop on one press. While
  targets execute, the first press names them and how long they have run, and a second
  within three seconds stops them.
