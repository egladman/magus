### Changed

- **BREAKING: `-o mermaid` is removed.** `magus graph deps`, `magus graph export --select`,
  `magus describe graph` and `magus run --graph` no longer emit Mermaid; `-o dot` stays.
  `-o mermaid` is now an unknown output format.
- **The advice action links the console instead of posting a Mermaid fence.** The
  blast-radius section links the hosted console, which draws the graph in the browser. The
  graph travels in the link's URL fragment, which a browser never sends to a server. The new
  `console-base` input points the link at a console of your own.
