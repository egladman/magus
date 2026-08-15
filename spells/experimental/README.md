# experimental

Things the maintainer is playing around with. Nothing in this directory ships in
the magus binary, nothing here is covered by a stability promise, and anything
here may change or disappear without notice. An experiment lives here precisely
because it is not yet clear whether it is worth keeping.

Each subdirectory says what it is:

- `nx/` - a workspace-provider spell that maps an Nx workspace into magus. Copy
  it into your own Nx repo and import it there; see
  [docs/guides/integrations/nx.md](../../docs/guides/integrations/nx.md) for setup.
