### Fixed

- **Switching a checkout between schema versions no longer strands `graph-generate`.**
  The downgrade guard in `fs.write_file` refused to overwrite a git-ignored export, such
  as `gen/knowledge-graph.json`, that a newer branch had stamped. It now regenerates a
  file the VCS ignores and still refuses a tracked one, or any file when the VCS cannot
  answer.
