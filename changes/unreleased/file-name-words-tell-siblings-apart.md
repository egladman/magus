### Added

- **A file name's extra words must tell it apart from a sibling.** The lint rule
  `file-name-words-tell-siblings-apart` reports a Go, TypeScript or Buzz file of two
  or more words whose first word no other file in its directory starts with, and
  suggests the first word alone. It reports only when the last word ends names in
  three directories and the file declares a type named by the first word. It reads
  the symbol index, so a stale index fails the rule.
