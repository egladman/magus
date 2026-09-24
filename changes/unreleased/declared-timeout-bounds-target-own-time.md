### Changed

- **A declared `timeout` bounds the target's own time.** A body parks while its
  `ctx.needs` dependencies run and gets a fresh deadline per stretch of its own work.
