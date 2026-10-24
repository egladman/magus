### Changed

- **A failed `magus affected ci` closes by naming each failed target again.** One line per failed target gives its output ref, its project and target, and the first line of its cause, so failures that scrolled away under the rest of the gate are one `magus query output <ref>` away. `hack/dev/run-gate.buzz` is gone: run `./magus status --wait | ./magus affected ci`, bound it with `magus job exec --timeout`, and make the checkout gate-ready with `hack/dev/bootstrap-checkout.buzz --gate-ready`.
