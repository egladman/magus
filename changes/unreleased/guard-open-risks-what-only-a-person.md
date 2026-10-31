### Fixed

- **What only a person may do never degrades on a guard timeout.** A ledger write and
  `job grant` meet their own rules, and a `magus run` not shown clear of `human_only` is
  denied, including when the read of the target policies is what ran out of time.
