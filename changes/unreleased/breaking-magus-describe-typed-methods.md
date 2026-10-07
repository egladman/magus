### Changed

- **Breaking: `magus\describe` is an object with one typed method per noun.**
  `magus\describe.file`, `.module`, `.spell`, `.charm`, `.target`, `.evaluatedTarget`,
  `.project`, `.evaluatedProject`, `.graph`, `.graphMarkdown`, `.workspace`, `.tool`,
  `.rule`, `.harness` and `.mcpTool` each return the record `magus describe <noun> -o json`
  prints. They replace `magus\describe(args)`, `magus\describeFile`,
  `magus\describeModule`, `magus\projects`, `magus\targets` and `magus\tools`; a noun
  with no method (`job`) is reached through `magus\cmd("describe", [...])`.
