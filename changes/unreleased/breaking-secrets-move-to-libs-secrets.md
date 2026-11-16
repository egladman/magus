### Changed

- **Breaking for SDK callers: secrets live in the `libs/secrets` module.** `types.Secret`,
  `NewSecret` and `SecretGrant` are `secrets.Secret`, `New` and `Grant`, and
  `internal/secret` is gone: its resolver is `secrets.NewResolver`, with `Provider`,
  `Redact`, `RedactString`, `NewRedactingHandler` and the endpoint forwarder beside it.
  `OpenEndpoint` takes its scope as an argument. A spell-backed provider resolves only
  through `magus.WithSecrets(secrets.WithOpener(...))`, which the CLI passes; a library
  caller that selects one without it gets an error on the first read. A refused grant is a
  `*secrets.GrantError`, still reported as MGS1027. No alias remains; change the import
  and the name. Buzz names are unchanged.
