### Added

- **A command rule can tell a push it cannot place.** `CommandRequest.unplaced` is true when
  the line's first push, pull request call or relink runs where only running the line shows,
  such as after `cd "$X"`. `checkout` is then null, and `dir` and `workspace` name where the
  line starts. This repository's policy denies such a line.

### Fixed

- **A heredoc append is judged as the file it leaves.** `cat >> f <<EOF` and `tee -a f` reached
  the write rules with the fragment as the whole file, so the comment rules graded every line
  of it as new. The write request now carries the file on disk with the body appended.
