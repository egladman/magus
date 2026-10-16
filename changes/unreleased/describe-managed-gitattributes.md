### Fixed

- **`magus describe file .gitattributes` calls a change to magus's own section maintained.**
  A project that declared `.gitattributes` made every change to it read as a hand edit of
  a source, even one confined to the section magus writes. Such a change now reports
  `maintained`; an edit elsewhere in the file still reports the declared role.
