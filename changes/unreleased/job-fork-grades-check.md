### Fixed

- **`magus job fork`, `job apply` and `magus\job.put` refuse a check the guard would deny
  the job's worker.** `go-build .` under `worker-builds-magus` is refused at the fork,
  quoting the verdict and naming `go::go-vet`. A workspace whose rules do not load, or a
  deny `go::go-vet` meets too, is not graded, and the fork says so.
