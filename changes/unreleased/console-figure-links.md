### Added

- **The console draws a dependency graph from a link.** `graph/#figure=<base64url JSON>`
  opens the Graph's Figures mode on the graph the link carries, drawn in the page with
  `magus/figure`. The fragment never reaches a server, so the hosted console needs none. A
  malformed link shows an inline notice naming the problem.
