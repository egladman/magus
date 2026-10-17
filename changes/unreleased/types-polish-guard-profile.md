### Changed

- **Breaking for SDK callers: `types.GuardProfile` has `Effective` and `Stricter`.** They replace `Level` and `StricterProfile`. `String` renders the profile as written, empty when unset, as `MarshalText` does.
