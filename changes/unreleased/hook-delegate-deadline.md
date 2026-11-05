### Fixed

- **A hook call delegated to another checkout's `./magus` is answered before the kill.**
  The delegate inherits the hook's deadline and answers unjudged on time; one killed
  anyway yields a deny naming it. The failure notice now names the binary that answered
  and the exit code of a silent one.
