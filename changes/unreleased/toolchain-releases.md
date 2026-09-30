### Added

- **A spell's tool names the endoflife.date product it follows.**
  `Tool{lifecycle = "nodejs"}` sits beside the version probe; the go, typescript, python
  and rust spells declare `go`, `nodejs`, `python` and `rust`. It carries no dates; the
  lifecycle provider looks them up by it.
