### Fixed

- **A lapsed guard judgment passes a write outside every checkout.** A guard that ran out
  of its budget judging `cat >> <file> <<EOF` denied it. A line whose only writes are
  redirects or `tee` files outside every git checkout and the workspace passes a
  lapse with a note. A push, a destructive verb or any other write stays denied.
