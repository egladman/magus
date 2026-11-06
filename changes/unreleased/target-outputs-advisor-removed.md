### Changed

- **Breaking: the `target-outputs` PR advisor and its action input are gone.** It guessed
  whether a new target with no outputs forgot them, which MGS4011 now answers at run time.
  Remove `target-outputs:` from any `with:` block that sets it.
