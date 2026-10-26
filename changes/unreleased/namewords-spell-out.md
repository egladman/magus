### Changed

- **Function and method names spell clipped words out.** `namewords` refuses a
  short form such as `Ctx`, `Cmd`, `Opts` or `Repo` on every function and method,
  exported or not. Existing names use the full word. Initialisms (`ID`, `URL`,
  `HTTP`) and the standard library's own spellings (`Dir`, `Env`, `Addr`) stay.
