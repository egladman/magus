### Security

- **Breaking: queue hooks run in the base's sandbox.** The gate, the regeneration and a
  `--facts` command run under the base's `sandbox` policy, at least `best-effort`,
  rooted at their checkout: landlock confines their files on Linux, and everywhere their
  environment is the sandbox's. `--sandbox=required` refuses a hook the kernel cannot
  confine; `tools/gha-queue.buzz` passes it.
