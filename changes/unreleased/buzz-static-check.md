### Added

- **Two lint rules check Buzz sources and diagnostic code pages.**
  `buzz-sources-pass-the-static-check` runs `magus buzz --check` over every tracked `.buzz`
  file and reports warnings as well as errors. `diagnostic-codes-have-docs-pages` fails a BZZ
  or MGS code with no docs page. The `magus-buzz-lang` skill now says the checker reports
  unknown methods and the `len` free function.
