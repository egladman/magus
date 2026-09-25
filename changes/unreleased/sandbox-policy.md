### Changed

- **Breaking: `sandbox.enabled` is now `sandbox.mode: off | best-effort | required`.**
  Also `MAGUS_SANDBOX` and `--sandbox=<mode>`, replacing `MAGUS_SANDBOX_ENABLED` and
  `--sandbox-enabled`. `best-effort` is the old `enabled: true`; `required` refuses to
  run (MGS2012) unless kernel landlock enforces the policy. The old env var is an
  error naming its replacement.
