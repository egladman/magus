### Changed

- **Breaking for SDK callers: `Run`, `RunCI`, `RunAffected` and `RunGate` are judged before they run.** Each is judged as the CLI line that reaches it (`magus run <target> <project>`, `magus affected <target>`, `magus affected ci`), and a deny returns a `*types.GuardDeniedError` carrying the judgment, with nothing run. The CLI is not judged twice: its host's hook already judged the invocation.
