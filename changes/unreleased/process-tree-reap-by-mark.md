### Changed

- **A command's leftover processes get SIGTERM before SIGKILL, found by a mark.** Each
  command's environment carries `MAGUS_PROC_TREE`. When the command exits, every process
  still carrying its mark gets SIGTERM, then SIGKILL after 500ms. The broker, the server
  and a detached `proc\exec` drop the mark, so they outlive the run.
