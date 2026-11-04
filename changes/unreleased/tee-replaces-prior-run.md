### Changed

- **`--tee` replaces output from an earlier invocation.** The first write clears the
  previous capture; later writes retain every record from the current invocation.
  Reusing a filename no longer mixes separate runs into one record stream.
