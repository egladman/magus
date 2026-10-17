### Added

- **`magus diff` flags a shared function with a caller-specific knob.** The `shared-loop` check, on by default as advice, names a function whose knob (a parameter its body branches on, or an option one caller alone uses) is past the coupling rule, and a function re-signed to branch on a parameter. Remedies: a copy per caller, or a `mode-knobs` entry.
