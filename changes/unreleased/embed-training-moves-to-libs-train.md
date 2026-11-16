### Changed

- **The embed trainer moves from `libs/embed/train` to `libs/train/embed`.** All model
  training lives in one module, `libs/train`, whose Go module is now
  `github.com/egladman/magus/libs/train`. The project is `libs/train`, so
  `magus run table libs/embed/train` becomes `magus run table libs/train`. `libs/embed`
  stays the shipped runtime. The old path and module name are gone with no forwarding path.
