### Fixed

- **`docker.io` references reach Docker Hub.** `docker.io` and `index.docker.io` requests
  go to `registry-1.docker.io`, the host that serves the API; a one-segment Docker Hub
  name such as `docker.io/alpine` is refused with its `docker.io/library/alpine` spelling.
