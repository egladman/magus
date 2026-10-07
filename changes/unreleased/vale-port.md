### Added

- **`magus\symbols()` returns every symbol's doc comment as data.** Each record
  carries the node, source, language, name, kind, owner and the whole comment, for
  every language whose symbol index is declared, beside each index's freshness. A
  magusfile rule reads it and decides what the text must say; magus judges none of it.
