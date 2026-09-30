### Fixed

- **Every guard verdict names its rule.** A lease, focus, hook-wiring, notes or
  cache-dir refusal, an undeclared lease, and five advisories recorded no rule. Each now
  carries a catalogued name: `lease-write`, `lease-undeclared`, `lease-gate`,
  `lease-vcs`, `lease-rebind`, `lease-harness`, `focus-read`, `hook-wiring-write`,
  `lease-state`, `dependency-update`, `dependency-install`, `echo-on-success` and
  `timed-magus`, each listed by `magus describe rules`.
