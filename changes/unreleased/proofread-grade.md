### Changed

- **proofread grades each text against a minimum for its kind.** Advice findings now cost
  a measured weight, and a text under its kind's minimum gets a `grade` deny finding. Set
  minimums per kind in a decisions file or with `--minimum N`. `--fail-on` is gone.
  `reply-voice`, `wordy`, `signpost`, `leak` and `changelog-group` no longer deny by default.
