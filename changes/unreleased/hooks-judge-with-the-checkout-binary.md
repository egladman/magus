### Fixed

- **A checkout's own `./magus` judges its hooks, however old.** A worker built before a
  `magus.yaml` key landed was locked out of every write. In a workspace that builds magus
  from source, PATH magus now answers only "bootstrap ./magus".
