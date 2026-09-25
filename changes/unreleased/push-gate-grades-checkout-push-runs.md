### Fixed

- **The push gate grades the checkout a push runs in.** `git -C <dir> push`, `hg -R`,
  `jj -R` and a leading `cd <dir> &&` are graded by that checkout's revision and gate
  record, not the hook's, and a single refspec grades the revision it names. A directory
  only the shell can resolve keeps the advisory.
