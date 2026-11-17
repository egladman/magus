### Changed

- **Breaking: `magus\cache.remote` and `magus\secret.provider` outside a magusfile
  evaluation are errors.** Called where no magusfile is being evaluated, such as a
  `magus buzz` script, `magus\cache.remote` used to do nothing and `magus\secret.provider`
  selected a provider for the script's own reads. Both now fail with "is a magusfile declaration".
- **Breaking: a workspace makes one secret-provider and one remote-cache choice.** Two
  magusfiles naming the same spell pass. Two naming different spells fail the load,
  naming both projects and both spells, where the last one evaluated used to win. One
  magusfile naming two different spells fails the same way.
- **A magusfile's top level reads secrets through its own provider only.** During load,
  a `magus\secret.read` in one project's magusfile used to go through a provider another
  project's magusfile declared earlier. It now goes through the provider its own
  magusfile selects, or the built-in environment provider. The chosen provider applies
  to every run once the load ends, so a magusfile that reads at its top level selects
  the provider itself.
