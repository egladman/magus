### Changed

- **`magus refs` names where a missed symbol appears as text.** When no symbol matches but
  the name is in the tree, the miss lists up to ten `path:line` sites and how many more
  there are, in the text block and in `-o json` as `answer.text.sites` and
  `answer.text.more`, so a worker with a stale index no longer has to search again.
