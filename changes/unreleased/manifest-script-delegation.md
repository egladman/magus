### Added

- **`magus doctor` reports a target that runs a manifest script (MGS1049).** A call such
  as `pnpm run build` or `poe lint` keeps its steps, inputs and outputs out of the cache
  key. Spells declare those argv prefixes with the new optional `mgs_listScriptRunners()`,
  and `typescript` and `python` declare theirs. Doctor traces each target's `proc\exec`
  under the dry-run host.
