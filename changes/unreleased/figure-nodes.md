### Added

- **Figures are graph nodes.** The graph build reads every `figure\of` call in Buzz source
  into a `figure:<id>` node with its title, eyebrow and description, and a `depicts` edge
  to each directory, and the project rooted there, that a `magus\dir` call in the same
  function names. `magus query 'kind=figure <question>'` finds the figure that answers a
  question. An argument that is not a string literal is listed in the node's `unresolved`
  attribute, and a figure with no literal id is keyed by its file and function.
