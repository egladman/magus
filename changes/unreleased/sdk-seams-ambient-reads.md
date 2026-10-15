### Changed

- **Breaking for SDK callers: a workspace opened without `WithEnv` reads no environment,** without `WithStderr` writes its notices nowhere, and without `WithCommandLine` records an empty command line. The CLI passes all three.
