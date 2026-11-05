### Changed

- **The finished-branch limit counts only unpublished work.** A finished job branch whose head
  a remote ref holds is carried by a pull request, so the spawn policy no longer counts it
  toward `finishedBranchLimit`. The denial says how many branches it skipped as published,
  and names why when a branch's remote refs could not be read, which then stays counted.
