### Changed

- **A job row pins its calls to its authority only while it is declared or running.** An ended row is judged from the working tree, so its id can be forked again at a newer commit. A dirty fork records `<revision>+<digest>`. Callers that read an ended row's authority break; no shim.
