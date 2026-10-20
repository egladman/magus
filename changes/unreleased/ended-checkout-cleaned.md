### Changed

- **An ended job's checkout is cleaned before magus decides whether it holds work.** Files
  magus wrote in it (declared outputs, maintained files, gofmt-only Go changes) are
  restored, and a checkout kept for real work names the uncommitted paths. A job ended by
  name also has its linked checkout restored and tried for removal, which says why it
  stays.
