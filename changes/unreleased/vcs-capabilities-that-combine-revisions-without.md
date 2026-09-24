### Added

- **VCS capabilities that combine revisions without a working copy.** `TreeReporter`,
  `TreeMerger`, `CommitWriter`, `GeneratedPathReporter`, `CheckoutProvisioner`,
  `RevisionFetcher`, `Pusher` and `Bundler` join `types.VCSDriver`, with `RangeFiles` and
  `RangeCommits` on `RangeReporter`. git implements every one; none runs a hook or signs.
