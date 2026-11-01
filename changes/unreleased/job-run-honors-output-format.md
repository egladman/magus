### Fixed

- **`job run` no longer ignores `-o`.** It accepted the flag and printed prose; it now prints
  a record of whether the job was submitted, coalesced, refused or had no server to go to.
