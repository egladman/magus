### Fixed

- **A nested magus aimed at another workspace runs there.** `magus --root <other> ...` run
  inside `magus run` was handed to the outer run, whose own lock refused it with MGS3007.
  The outer run now declines a run from another workspace, so the nested magus runs it
  under that workspace's locks. A nested run into the same workspace is still refused.
