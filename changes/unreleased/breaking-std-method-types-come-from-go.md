### Changed

- **Breaking: a `std.Method` takes its types from its Go signature.** `Arg.Type` and
  `Ret.Type` are filled when the module is registered, so a descriptor no longer sets
  them, and `Method.Extern` holds a nil value of the bound Go func type instead of
  `true`, so an extern is checked against a signature as an Impl is.
