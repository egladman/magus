### Fixed

- **A hook call delegated to another checkout's `./magus` is answered before the kill.**
  The delegate inherits the hook's deadline, refuses one past or beyond the budget,
  and hands neither it nor the delegation to what it spawns. One killed before
  writing yields a deny naming it. The failure notice names the answering binary
  and a silent one's exit code.
