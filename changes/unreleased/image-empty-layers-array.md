### Fixed

- **An image with no layer writes `"layers":[]`.** It wrote `"layers":null`, which
  image-spec does not allow.
