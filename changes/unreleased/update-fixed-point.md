### Fixed

- **A formatter no longer passes from the cache over a file it would change.** A run
  that rewrote a `ctx.modifiesExistingFiles` file was cached under the bytes it started
  from, so those bytes coming back hit and left the file unformatted. Such a run is now
  cached only when it leaves every modified file unchanged.
