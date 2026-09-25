### Removed

- **Breaking: `magus run --then` and `magus affected --then`, with no alias.** Pipe the
  run into a `magus buzz` script instead: `pipe\outputs`, `pipe\exportTo`,
  `pipe\history`, `pipe\diff` and `pipe\value` act on the records it reads, and
  `fs\readFile` and `crypto\sha256File` cover `contents` and `hash`.
