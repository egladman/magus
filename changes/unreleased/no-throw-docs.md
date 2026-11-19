### Changed

- **The docs site generator and the agent glue return errors as values.** The diagram
  builders, the page engine, the site pages, `lib/` and the agent hook glue return a
  `Result` instead of declaring `!>`, and the docs no-throw baseline is deleted. A failed
  render still stops the target with the same message. One baseline row stays: the
  OpenCode plugin test's `Bun.spawn` double must throw, as the platform does.
