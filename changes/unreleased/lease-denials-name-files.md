### Fixed

- **The lease gate names the files a rewrite would touch outside the row.** A
  run refused for writing past a lease's write paths now lists those globs in
  the verdict line itself, so the worker knows which to claim.
- **A leased worker's foreign-binary remedy names the checkout it holds.** The
  foreign-binary and go-into-checkout refusals sent a leased worker to
  `<other>/magus --root <other>`, which lease-checkout then refused. They now
  name the checkout the lease holds.
- **A heredoc append is judged as the file it leaves.** `cat >> f <<EOF` and `tee -a f` reached
  the write rules with the fragment as the whole file, so the comment rules graded every line
  of it as new. The write request now carries the file on disk with the body appended.
