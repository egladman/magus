### Fixed

- **Lint rules use the renamed finding hint field.** Rule constructors, reads,
  tests and the required-field check use `hint`, so the lint runner loads after
  the diagnostic field rename.
- **Generated TypeScript errors carry diagnostic hints.** `MagusError` copies
  `Diagnostic.hint` in both generated mirrors. Their generation inputs include
  the emitter and nested domain type sources.
- **Proofread decision callers unwrap their Result.** Generation writes the
  decision table rather than a Result envelope, and the evidence lint reads the
  returned table after checking its error.
