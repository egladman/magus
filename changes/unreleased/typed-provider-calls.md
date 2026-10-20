### Changed

- **Breaking: secret, CI, cache and lifecycle provider ops take a typed request.** Write
  `fun op(target: Target, req: Req) > Resp` (`resolve_secret(target, req: SecretRef) > Secret`,
  `get_artifact(target, req: ArtifactRequest) > Ack`); an op still taking `cb: fun(any)`, or
  returning a bare `str`, fails to load with MGS1051.
