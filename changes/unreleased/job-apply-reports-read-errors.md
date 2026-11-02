### Changed

- **`magus job apply` exits 1 on a failed read.** A record file, the job store or a VCS
  read that fails exits 1 with the underlying error; exit 2 stays for a misused command
  line, including `--add-write-path` naming an unknown job.
