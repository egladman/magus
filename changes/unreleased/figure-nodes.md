### Added

- **Figures are graph nodes.** Each `figure\of` call in Buzz becomes a `figure:<id>`
  node with its title, eyebrow and description, and a `depicts` edge to each directory
  a `magus\dir` call in the same function names. `magus query 'kind=figure <question>'`
  finds the figure that answers a question. A non-literal argument is listed in the
  node's `unresolved` attribute.
