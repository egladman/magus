### Changed

- **A `go.mod` no longer roots a workspace.** Without a `magus.yaml` or magusfile, the root is
  the nearest VCS checkout (`.jj`, `.hg`, `.sl` or `.git`), and magus warns that it fell back
  and names `magus init`. A directory with none of these fails with MGS3008.
