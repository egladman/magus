### Changed

- **Magusfiles and every embedded Buzz session reject `mod.member`.** The dot form on
  an imported module now fails at load the way upstream fails it, `` `fs` is not
  defined ``, and the error names the fix, `fs\writeFile`. An imported spell, project or
  remote spell handle is a value and keeps its dot (`markdown.markdownlint(ctx)`,
  `lint.name`).
