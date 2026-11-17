### Added

- **gopherbuzz's `HeapStats` counts buffered strings.** `BufferedStrings` and
  `BufferedBytes` count the long strings `+` and interpolation extend on a shared buffer
  and the arrays those buffers allocate. Under the default build each keeps its slot for
  the process, so a long string rebuilt again and again shows here.
