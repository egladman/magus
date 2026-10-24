---
title: Provider I/O is Buzz
order: 8
description: A CI or VCS provider is reached only by a Buzz op that names the act; Go opens no provider socket of its own.
tags: [design, judgment, providers, buzz]
---

# Provider I/O is Buzz

A CI or VCS provider (GitHub, GitLab, ...) is reached only by a Buzz op magus
invokes (a provider spell, the queue's own provider script, a `hack/` driver
script, or a workflow action step), which names the act and reports host and
elapsed on the line. Go opens no provider socket of its own; it calls the Buzz
op through bindings and reads the record back, the shape `review_spell.go` and
the queue's own Buzz host module already take. The console reaches nothing but
its own daemon, which its CSP enforces at runtime.

The failure this prevents is environment sniffing's shape from the other
side: a "quick fetch" landing in Go, or in a Buzz file nobody meant to carry
one, stays invisible until the day the credential it silently depended on is
missing or wrong, by which point it is load-bearing.

## Enforced by

- `providerio` in [`libs/conventions`](https://github.com/egladman/magus/tree/main/libs/conventions) fails a `net/http` client or a provider
  SDK import outside a reasoned allowlist.
- `provider-io-is-buzz` in [`hack/lint/run.buzz`](https://github.com/egladman/magus/blob/main/hack/lint/run.buzz) fails a real `gh`, `curl` or
  `http` call at a provider host from a Buzz file outside the ones named above.
- The console's CSP allows only its own daemon.

## Good

The provider call lives in a Buzz op that names the act, and Go reads the
record it returns.

```text
provider spell op -> host and elapsed on the line -> typed record back to Go
```

## Bad

```go
resp, err := http.Get("https://api.github.com/repos/" + repo + "/pulls")
```

A fetch in Go depends on a token nobody declared, reports nothing on the line,
and fails far from the change that added it.

## See also

- [Told, never guessed](told-never-guessed.md)
- [Providers](../../concepts/providers.md)
- [ADR 0011](../../decisions/0011-typed-provider-calls.md)
