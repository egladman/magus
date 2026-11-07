### Fixed

- **The conformance advice counts only the names it lists.** It counted every check
  the review carried, change-risk included, so a pull request with no diverging name
  read "A name diverges from the workspace (25)" over an empty section.
