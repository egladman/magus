### Changed

- **`job fork --checkout` copies your `./magus` when no Go build input differs.** A fork
  whose checkpoint is past the commit `./magus` was built at no longer drops the binary
  when the only changes are test files and changelog fragments; otherwise it still prints
  the bootstrap line.
- **`job rm` ends the checkout its row owns.** A clean checkout is removed with the row; one
  holding uncommitted work or commits on no branch stays, and so does its row, with the
  reason in the error.
- **A `job fork --checkout` lands on a branch named after the job.** It was detached, so
  its worker could not commit on a branch and none could be made there. git checks out a
  new branch, hg an active bookmark shared with the source, and jj names the workspace
  after the job. A name already taken is refused.
