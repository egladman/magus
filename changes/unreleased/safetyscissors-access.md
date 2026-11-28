### Added

- **`libs/safetyscissors/access` judges who may do what by attribute rules.** A
  `Request[P, T, D]` carries the caller's own principal, target and detail types. A
  `Rule` from `Permit` or `Forbid` names one act, a condition and a one-line
  description. The package knows no role and imports nothing of magus.
- **A `Table` grows add-only.** `New` and `Table.Extend` join a `Bundle` of acts and
  rules. A later bundle permits only its own acts and may forbid any.
- **`Table.Judge` never answers yes.** A forbid beats a permit, and an act no permit
  allows is denied with a reason listing each permit. Every error comes with a deny.
- **Typed refusals.** `*UndeclaredActError`, `*RedeclaredActError`,
  `*DuplicateRuleError`, `*DuplicateBundleError`, `*ForeignPermitError`,
  `*DeclarationError` and `*ConditionError`.
