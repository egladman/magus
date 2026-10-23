### Changed

- **MGS7003 names a magus built at another commit than the checkout.** When magus builds
  itself and the cache records no run for the symbol index it wrote, the error now says which
  commit the binary was built at and which the checkout is at, and names the remedy that
  clears it: build `./magus` at HEAD, run `magus server stop --pools`, then
  `magus graph build`. A graph build alone does not.
