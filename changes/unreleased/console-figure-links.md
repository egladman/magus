### Added

- **The console draws a dependency graph from a link.** `graph/#figure=<base64url JSON>` opens
  the Graph's Figures mode on the graph the link carries, drawn in the page with `magus/figure`
  through the playground runtime. The fragment never reaches a server, so the hosted console
  needs none. An edited project is tagged and accented; an unknown version or a payload that is
  not base64url JSON shows an inline notice naming the problem.

### Removed

- **The console's last Mermaid mentions.** The dead "Copy as Mermaid" styles and the comments
  that cited the Mermaid emitter are gone.
