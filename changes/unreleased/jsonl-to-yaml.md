### Changed

- **Breaking: the SDK allow list and the `libs/train` pins are YAML.**
  `hack/lint/sdk-exports.jsonl` is `hack/lint/sdk-exports.yaml`, a map of exported name to
  reason. `libs/train/inputs.jsonl` and `libs/train/embed/inputs.jsonl` are `inputs.yaml`
  files, a list of pinned entries; `embed` and `train` refuse a field they do not know. A
  fork carrying its own allow list or pins converts them by hand.
