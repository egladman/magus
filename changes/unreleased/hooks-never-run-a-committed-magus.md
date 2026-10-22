### Security

- **Hooks and the merge driver never run a `magus` the repository commits.** A tracked
  `./magus` is skipped for the merge driver, and a hook prints why instead of running it.
  Installing hooks into a `core.hooksPath` outside the repository is refused.
