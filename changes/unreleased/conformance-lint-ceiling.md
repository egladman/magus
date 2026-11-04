### Added

- **This repository holds a change to the conformance ceiling.** `lint` fails when a file the change touches holds more violations of a checked metric than it held at the base revision (`hack/lint/code-stays-within-the-conformance-ceiling.buzz`); the checked metrics are typed in `hack/magusfile/conformance-ceiling.buzz`. A drop is progress. Every other metric is printed as advice.
