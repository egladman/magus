### Fixed

- **The bootstrap builds without mise.** It is now
  `GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .`; magus
  does not compile without that experiment, and the old command worked only where the
  mise shim set it. The `raw-tool` rule exempts this exact line; any other spelling is
  refused and served it.
