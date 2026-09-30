### Fixed

- **`interpreter-rewrite` judges what a script writes, not what it mentions.** A
  script that carries tracked paths as data and writes its report to scratch or
  stdout runs; only a write's destination is checked against the tree.
