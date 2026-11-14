### Added

- **A file name's extra words must tell it apart from a sibling.** The lint rule
  `file-name-words-tell-siblings-apart` reports a two-word file whose first word no
  sibling starts with, when the last word is a qualifier used in three directories
  and a type in the file starts with the first word. A stale symbol index fails it.
