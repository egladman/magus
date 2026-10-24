### Changed

- **A failed `magus affected ci` names each failed target again.** One line per target
  gives its output ref, project, target and first cause line. `hack/dev/run-gate.buzz` is
  gone: run `./magus status --wait | ./magus affected ci`, after
  `hack/dev/bootstrap-checkout.buzz --gate-ready`.
