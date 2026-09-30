### Added

- **`magus doctor` reports a target that runs a manifest script (MGS1049).** A call
  such as `pnpm run build` or `poe lint` keeps the script's steps, inputs and outputs
  in the manifest, out of the cache key. Spells declare which argv prefixes run a
  manifest script through the new optional `mgs_listScriptRunners() > [Command]`
  export; the `python` spell declares `poe`, `hatch run`, `pdm run` and `pipenv run`.
  Doctor finds the calls by tracing `proc\exec` in each target body under the dry-run
  host, which now records every `proc\exec` argv.
