### Changed

- **Breaking: `magus\cache.remote` and `magus\secret.provider` outside a magusfile
  evaluation are errors.** In a `magus buzz` script the first did nothing and the second
  selected a provider. Both now fail with "is a magusfile declaration".
- **Breaking: a workspace makes one secret-provider and one remote-cache choice.** Two
  magusfiles naming different spells fail the load, naming both projects and spells,
  where the last one evaluated won. The same spell twice passes.
- **A magusfile's top level reads secrets through its own provider only.** A load-time
  `magus\secret.read` used another project's earlier provider; it now uses its own
  magusfile's, or the built-in environment provider. The choice applies to every run once
  the load ends.
