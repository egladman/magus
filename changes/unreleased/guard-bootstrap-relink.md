### Fixed

- **A checkout that cannot load its own sources can be relinked by an agent.** When the
  workspace fails to load with MGS1021, the raw-tool rule allows the relink MGS1021 prints
  (`go build [-trimpath] -o magus ./cmd/magus`) and the generators the `*_generate`
  targets run, alone on their line. Everything else stays denied, and denied again once
  the workspace loads.
