### Fixed

- **A checkout that cannot load its own sources can be relinked by an agent.** When
  the workspace fails to load with MGS1021, the guard's raw-tool rule allows the relink
  MGS1021 prints (`go build [-trimpath] -o magus ./cmd/magus`, a `GOEXPERIMENT=` prefix
  allowed) and the generators the `*_generate` targets run (`go generate <pkg>`,
  `go run ./cmd/magus-utils <generator>`), alone on their line. Everything else stays
  denied, and all of it is denied again once the workspace loads. MGS1021's advice for a
  merged tree whose generated files lag its sources names those generators in order,
  where it used to say to restore them from the revision that generated them.
