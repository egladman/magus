### Fixed

- **A discriminated union can assert its whole object.** The TypeScript switch
  rule accepts an `unreachable` assertion on the immediate containing object
  after a property or indexed switch. Unrelated assertions, missing assertions
  and default arms still fail.
