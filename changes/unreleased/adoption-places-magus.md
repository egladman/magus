### Changed

- **`magus job exec` readies a checkout the host made before it takes the job there.** It
  fast-forwards the checkout to the job's checkpoint, refusing uncommitted work. It copies
  in a `./magus` built cleanly at that commit, or builds one with the `checkout.bootstrap`
  hook, pins it on the job's row and says on stderr what it did.
- **A checkout a fork or `job exec` readies gets the proofread binary.** The
  `checkout.bootstrap` hook's `--checkout` path now runs `proofread-build`, replaying a
  cached build where one stands, so the comment judge no longer refuses every edit there.
- **A fork reuses a `./magus` another job checkout built at its base.** Asking a candidate
  binary its version no longer hands the call to a pinned `./magus` above the working
  directory, which answered for it and sent the fork to a cold build.
