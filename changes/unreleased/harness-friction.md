### Fixed

- **A run summary names each failed target and its output ref.** The lines follow the
  summary, with the `magus query output <ref>` legend, so a log cut in the middle still
  says which ref to read. The plain terminal prints them too.
- **A landed job row ends even when its holder filed no result.** The checkout's diff
  decides whether the work reached the base branch, so a running or exited row with no
  result is no longer kept live.
- **The no-binary stale hint names the line that readies the checkout.** A fresh checkout
  is told to bootstrap `./magus` and then run the checkout's verify-ready line, which
  installs the tool rows the build alone leaves out.
- **`hack/ci/show-failures.buzz` writes its gh-log fallback to stderr.** Stdout carries
  output records only.
- **A merge commit's comments are graded against both parents.** A comment the merged
  branch already holds is not counted as added by the merge.
