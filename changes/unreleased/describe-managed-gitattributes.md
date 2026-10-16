### Fixed

- **`magus describe file .gitattributes` calls a change to magus's own section maintained.**
  A project that declared `.gitattributes` made every change to it read as a hand edit of
  a source, even when the only difference from the committed file was the section magus
  writes. A change confined to that section now reports `maintained`; an edit anywhere
  else in the file still reports the declared role.
