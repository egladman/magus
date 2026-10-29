### Fixed

- **A job checkout's own `./magus` judges calls that land in it.** A branch that extends types or bindings no longer locks its own checkout out of `:rw` runs.
