### Changed

- **The advice action links the console instead of posting a Mermaid fence.** The
  blast-radius section links the hosted console, which draws the graph in the browser from
  the link's URL fragment, which is never sent to a server. The new `console-base` input
  points the link at a console of your own.
