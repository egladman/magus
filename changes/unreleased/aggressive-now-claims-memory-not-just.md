### Changed

- **`aggressive` now claims memory, not just cores.** `balanced` and `conservative`
  still reserve a quarter of memory for everything else on the machine; `aggressive`
  takes every usable megabyte down to a fixed 512 MiB floor for the kernel.
