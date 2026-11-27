### Changed

- **Breaking: `match` over an enum follows upstream Buzz's exhaustiveness rules.** A bare enum case over an optional subject is refused, and an optional enum needs an `else` or an unwrap. Also reworks the shipped `magus-multi-agent` skill: it is trimmed to 500 lines, with its budget, validation and split guidance rewritten.
