### Fixed

- **`go::go-clean` accepts forwarded flags.** `-cache`, `-testcache`, `-modcache`, and
  `-fuzzcache` always landed alongside `./...`, which go refuses in any order. A new
  `Command.TrailingArgs` slot drops the package pattern the moment any flag is
  forwarded; a bare `go::go-clean` still cleans `./...`.
