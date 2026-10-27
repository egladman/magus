# image assembly examples

Examples mapped from [Docker Official Images](https://github.com/docker-library/official-images)
and related Official Image Dockerfiles. Each file states the upstream reference, what
magus expresses, and what stays outside the `image` module.

| Example | Upstream | Complexity | Magus role |
| --- | --- | --- | --- |
| [hello-world.buzz](hello-world.buzz) | [docker-library/hello-world](https://github.com/docker-library/hello-world) `Dockerfile-linux.template` | scratch + one binary | full assemble |
| [busybox.buzz](busybox.buzz) | [docker-library/busybox](https://github.com/docker-library/busybox) packaging `Dockerfile` (`ADD rootfs`) | scratch + rootfs tree | DirPlacement `strip=1` after host unpack |
| [static-go.buzz](static-go.buzz) | golang multi-stage *app* shape (not the toolchain image) | target builds, assemble packages | packaging stage only |
| [multi-arch.buzz](multi-arch.buzz) | Official images' multi-arch Index pattern | two layouts → Index | `assembleIndex` |
| [config-overlay.buzz](config-overlay.buzz) | nginx/httpd after apt: COPY conf + entrypoint | digest-pinned base + files | one Layer over base |
| [pull-base.buzz](pull-base.buzz) | (stance) offline base + `:update` refresh | local layout vs `pullLayout` | `update` charm gate |
| [target-private-git.buzz](target-private-git.buzz) | BuildKit SSH mount *need*, without an image mount verb | target + agent; then place | seam doc |
| [nginx-non-goal.buzz](nginx-non-goal.buzz) | [nginxinc/docker-nginx](https://github.com/nginxinc/docker-nginx) apt/runtime | package install | documents refusal |
| [redis-non-goal.buzz](redis-non-goal.buzz) | [docker-library/redis](https://github.com/docker-library/redis) 7.4 debian | apt + compile-from-source | documents refusal |

Stance: [Assemble an OCI image with magus](../../../docs/guides/setup/assemble-oci-image.md).  
Decision: [ADR 0018](../../../docs/decisions/0018-magus-assembles-oci-images.md).

These snippets illustrate the `image` host module. Type-check with
`magus buzz --check std/examples/image/<file>.buzz`.
