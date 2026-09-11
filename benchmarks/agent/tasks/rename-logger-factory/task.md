# Rename createLogger to makeLogger

`packages/platform/logging` exports a factory named `createLogger`. Rename it to
`makeLogger` and update every caller across the repo. When you are done the name
`createLogger` must not appear anywhere in the tree, and behavior must be unchanged.

Several packages carry a `gen/api.md` summary listing the names they export and the
names they import from other packages. Those summaries have to end up consistent
with the source. They are produced by tooling in this repo; do not hand-edit them.

The tests in the platform packages must still pass.
