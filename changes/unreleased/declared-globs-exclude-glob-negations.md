### Changed

- **`ctx.glob` refuses a pattern list that is only negations.**
  `ctx.glob("!site-generate")` used to select no targets; it now fails the run, like a
  declared glob made only of exclusions.
