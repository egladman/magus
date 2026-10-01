### Changed

- **Magusfiles and every embedded Buzz session reject `mod.member`.** The dot form on
  an imported module now fails at load the way upstream fails it, `` `fs` is not
  defined ``, and the error names the fix, `fs\writeFile`. An imported spell, project or
  remote spell handle is a value and keeps its dot (`markdown.markdownlint(ctx)`,
  `lint.name`).
- **`ns\object\member` is rejected, as upstream rejects it.** A namespace object's
  members take a dot: `magus\job.list()`, `magus\secret.read(ref)`. The error names the
  dot form.
- **The language server completes, hovers and signs `fs\glob`.** It read only `fs.glob`,
  the spelling the checker refuses; after a dot it offers nothing, since only a value's
  member follows one.
