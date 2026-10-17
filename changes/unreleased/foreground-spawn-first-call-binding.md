### Fixed

- **A foreground spawn's worker is bound to its job on its first call.** A host that sends no start for a foreground child no longer leaves it refused by `sibling-checkout` in the checkout its job owns.
