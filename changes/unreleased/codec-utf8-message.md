### Fixed

- **A marshal that refuses invalid UTF-8 in a RawMessage says it was writing.** The v1
  encoder reported the fault as `reading a string`, the decoder's wording. It now says
  `writing a string`, as the other invalid-UTF-8 refusals already do.
