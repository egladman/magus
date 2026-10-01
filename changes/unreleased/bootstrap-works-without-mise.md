### Fixed

- **The bootstrap builds without mise.** It now carries its own experiment:
  `GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .`. magus
  refuses to compile without `GOEXPERIMENT=jsonv2`, and the old command worked only where
  the mise shim set it. The `raw-tool` rule exempts this line alone, with no wrapper and
  no other prefix; without the prefix it is refused and served the full command.
