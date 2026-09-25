### Fixed

- **Guard advisories no longer fire on paths outside the workspace.** A command whose every
  path lies outside the root, or a write into a scratch directory, is advised nothing, and
  scripted-rewrite no longer refuses a script whose every named path is outside it.
