### Added

- **`libs/safetyscissors/access` judges who may do what by attribute rules.** A
  `Request[P, T, D]` carries the caller's own principal, target and detail types. A
  `Rule` from `Permit` or `Forbid` names one act, a condition and a one-line
  description. The package knows no role and imports nothing of magus.
- **A `RuleSet` grows add-only.** `New` and `RuleSet.Extend` join a `Scope` of acts and
  rules. A later scope permits only its own acts and may forbid any.
- **`RuleSet.Judge` never answers yes.** A forbid beats a permit, and an act no permit
  allows is denied with a reason listing each permit. Every error comes with a deny.
- **Typed refusals.** `*UndeclaredActError`, `*RedeclaredActError`,
  `*DuplicateRuleError`, `*DuplicateScopeError`, `*ForeignPermitError`,
  `*DeclarationError` and `*ConditionError`.
