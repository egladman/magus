### Changed

- **A refused fork leads with its problems.** `magus job fork` prints what a fork leaves waiting only after validation passes.
- **`magus job apply` exits 1 on a failed read.** A record file, the job store or a VCS read that fails exits 1 with the underlying error; exit 2 stays for a misused command line, including `--add-write-path` naming an unknown job.
- **`magus ls jobs -o name` skips the unmerged read.** It never printed the answer.

### Fixed

- **`magus job apply -h` prints its examples once.**
- **A fork names a config that will not load.** It says the output store could not be located, instead of reporting that the store holds no passed full test.
- **The guard locates a script's own calls.** A line's script is judged only from the directories that call of it runs in, not from those of a same-named script at another path. A call reached through a wrapper still matches by name.
