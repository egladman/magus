### Fixed

- **A step cancelled after its work succeeded reports its end.** Observers and the output
  record now see it finish instead of it vanishing mid-run.
- **A status line keeps a file name's leading and trailing spaces,** and a line holding only
  the status prefix is no longer returned as a path.
- **A failed write of the job landing-probe history or the stored unmerged answers is
  logged** instead of dropped.
- **The in-flight record appends through the shared file primitive,** and the retired
  `.json.tmp.` temp name is no longer collected as litter.
