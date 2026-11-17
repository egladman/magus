### Changed

- **Breaking for SDK callers: secrets live in the `libs/secrets` module.** `types.Secret`,
  `NewSecret` and `SecretGrant` are `secrets.Secret`, `New` and `Grant`, and
  `internal/secret` is gone. Its resolver is `secrets.NewResolver`, with the providers,
  redaction and endpoint forwarder beside it. Buzz names are unchanged.
- **Breaking for SDK callers: a spell-backed provider resolves only through
  `magus.WithSecrets(secrets.WithOpener(...))`.** A library caller that selects one
  without it gets an error on the first read. `OpenEndpoint` takes its scope.
- **Breaking for SDK callers: a resolver reads through one provider.**
  `Resolver.WithProvider(name)` derives one that shares its kept answers and redaction
  set; nothing selects a provider in place. The context helper is `secrets.WithResolver`.
- **Breaking for SDK callers: every secrets error is typed and starts `secrets:`.**
  `*GrantError` is still MGS1027, beside `*ReadError`, `*EndpointError` and
  `*NoResolverError`. An IPv6 grant host keeps its brackets.
