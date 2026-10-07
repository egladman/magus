### Changed

- **Breaking: guard rules that refused a recoverable call now advise.** Twenty-three
  compiled rules, such as `stage-all`, `raw-tool` and `read-navigation`, serve the
  command that answers the call instead of denying it; a deny now means it cannot be
  undone. Set any rule by name with `magus\guard.builtins` in the root magusfile.
