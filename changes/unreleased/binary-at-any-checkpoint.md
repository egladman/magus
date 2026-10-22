### Changed

- **A job checkout forks at any checkpoint.** `magus job fork --checkout` no longer
  refuses a checkpoint the forker's ./magus was not built at. It makes the checkout with no
  binary and names `hack/dev/bootstrap-checkout.buzz`, which builds one there at that
  revision. The guard lets the root and the job's holder run it.
