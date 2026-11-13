### Changed

- **Breaking: the `types/agent` mirrors moved to the `magus\agent` namespace.**
  `magus\AgentSpec`, `magus\AgentModel`, `magus\AgentWorker` and `magus\AgentStep` are
  `magus\agent\Spec`, `magus\agent\Model`, `magus\agent\Worker` and `magus\agent\Step`.
  `StepRequest`, `JobModelTier`, `ModelTierBounds`, `SkillVariant`, `WorkerType` and
  `ModelTierSource` are `magus\agent\StepRequest` and so on. A script that names one adds
  `import "magus/agent";`, and `import "magus";` does not bring it. A guard step rule
  annotates `fun (req: magus\agent\StepRequest)`.
- **Breaking: the `types/trail` mirrors moved to the `magus\trail` namespace.**
  `magus\TrailMark`, `TrailObservation`, `TrailSpawn`, `TrailSection` and `TrailVerdict`
  are `magus\trail\Mark`, `magus\trail\Observation`, `magus\trail\Spawn`,
  `magus\trail\Section` and `magus\trail\Verdict`. `Trail` is `magus\trail\Trail`, and
  `magus\SessionOptions` is `magus\trail\ReadOptions`. `magus\session()` and its
  `Session` handle stay in `magus`. A script that names one adds `import "magus/trail";`.
- **Breaking: gopherbuzz binds a declaration module that declares a nested namespace
  under that namespace only.** `import "shop/order";` for a module declaring
  `namespace shop\order;` binds `shop\order`, not `order`, and a file names its types only
  through its own import, as upstream Buzz binds them.
- **`.buzzgen.yaml` takes a top-level `module`.** A package below a listed one is the
  nested namespace `<module>\<package>` unless its config sets `module`, and `module: ""`
  keeps its types where they are reached.
