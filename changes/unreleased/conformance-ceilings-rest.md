### Changed

- **`export-scope` counts use with no name.** An export is used when code outside its package reads one of its fields or methods, or reads a symbol whose signature carries it. Constants of one type count as one unit. A test or generated file reading it does not count.
