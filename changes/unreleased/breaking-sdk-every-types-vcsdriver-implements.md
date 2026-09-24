### Changed

- **Breaking (SDK): every `types.VCSDriver` implements every capability.** A backend
  without one returns `*types.VCSUnsupportedError` naming itself and a `VCSCapability`,
  matching `ErrVCSUnsupported` and `errors.ErrUnsupported`. `RemoteURL` takes a remote
  name, `RangeDiffReporter` is `RangeReporter`, and `Bisect` moves to `Bisector`.
