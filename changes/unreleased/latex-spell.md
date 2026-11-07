### Added

- **A `latex` spell builds, lints and formats LaTeX projects.** A `Tectonic.toml` roots a
  project whose `build` runs `tectonic -X build` into a declared `build/` PDF, with
  `chktex` for `lint` and `latexindent` for `format`. mise pins tectonic and latexindent;
  chktex needs a system install. The first build fetches the TeX bundle over the network.
