### Fixed

- **Lost work no longer reads as landed.** A rename, a mode change or an edited binary sharing an
  author and subject with another commit stays missing. A landed row carries `landed`, so a row a
  probe ended earlier is checked once more. A goal's argument naming no file is compared verbatim.
  `hg` and `jj` can fast-forward a checkout.
