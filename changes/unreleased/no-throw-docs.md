### Changed

- **The docs site generator and the agent glue return errors as values.** The diagram
  builders, page engine, site pages, `lib/` and agent hooks return a `Result` instead of
  declaring `!>`. Failed renders still stop the target. The docs no-throw baseline has
  one remaining row: the OpenCode plugin test's `Bun.spawn` double throws, as the
  platform does.
