### Added

- **Eight architecture figures drawn from the graph.** The guard's internals, the job record
  and its leases, the sandbox, the symbol index, the harness integrations, the merge queue's
  workflows, the CI and CD split and the cache's internals each get a figure, placed on the
  page that explains the subsystem. Every box is a directory the graph holds.

- **Nine more figures from the graph.** Doctor, the bindings generator, services, config loading,
  sessions, the dry run, the language service, the shared types and the VCS interface each get
  a figure on the page that explains them.

### Changed

- **The guard, lease, knowledge graph, agent and server figures show the subsystems as they
  stand.** The guard is drawn with the records it reads instead of three boxes, and the lease
  loop shows the job store, the spec and the guard beside the verifier. The knowledge graph
  splits out the symbol index, the agent figures show the harness spells, the skills install
  and the sandbox's landlock half, and the merge queue figure names the queue workflows.
  The OCI assembly figure now draws `libs/image` and `libs/oci`, the share figure draws the
  console's handler layer, and the server-jobs figure draws the watchers, registry and graph
  build.

### Removed

- **The harness-layer figure.** `agent-integration` carries what it drew, the guard and the
  job store inside the one Go module.
