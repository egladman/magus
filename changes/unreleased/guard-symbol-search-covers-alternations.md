### Changed

- **`symbol-search` denies alternations, definition lookups and diagnostic codes.** A
  recursive search whose every alternative (`A\|B`, `-e A -e B`, `func X`, `type X`) is an
  indexed symbol routes to one `magus refs <name> --occurrences` each; a registered MGS code
  routes to `magus explain diagnostic:<code>`.
