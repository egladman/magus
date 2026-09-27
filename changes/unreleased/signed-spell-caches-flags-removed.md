### Removed

- **`magus config cache export --toolchain` and `--used-within`, and `import
  --toolchain`.** `--remote` carries every cache the workspace's spells declare, and a
  save keeps only what was used since the last restore, with no flag to say so.
