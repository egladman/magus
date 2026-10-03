### Fixed

- **A namespace a module never imported is BZZ1009.** `magus\Context` without
  `import "magus";` named the wrong cure, `magus self update`; now the error says no
  import binds `magus` and names the line to add. Hints suggesting a target now carry
  that import.
