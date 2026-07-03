---
title: env module
description: Process environment variable access.
tags: [env, module, stdlib, magusfile]
---

# env

Process environment variable access.

> **Naming convention:** import the module under its bare name (`import "env"`) and call methods in `camelCase` (`env.someMethod`).

## Methods

### get

Return the value of name, or "" if unset. Use lookup to tell unset from set-but-empty.

**Signature:** `env.get(name) → string`[^buzz-stdlib-env-get] · [source](https://github.com/egladman/magus/blob/main/std/env.go#L91)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |

**Returns:** string

### lookup

Return (value, found); found is false when name is unset or stripped by the sandbox.

**Signature:** `env.lookup(name) → string, bool`[^buzz-stdlib-env-lookup] · [source](https://github.com/egladman/magus/blob/main/std/env.go#L107)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |

**Returns:** string, bool

### set

Set name to value in the current process environment.

**Signature:** `env.set(name, value)` · [source](https://github.com/egladman/magus/blob/main/std/env.go#L116)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |
| `value` | `string` |  | |

### list

Return all environment variables as a name→value map.

**Signature:** `env.list() → map[string]string` · [source](https://github.com/egladman/magus/blob/main/std/env.go#L198)

**Returns:** map[string]string

### unset

Remove name from the current process environment.

**Signature:** `env.unset(name)` · [source](https://github.com/egladman/magus/blob/main/std/env.go#L134)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |

### expand

Replace $VAR and ${VAR} references in s with their values (sandbox-stripped names expand to "").

**Signature:** `env.expand(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/env.go#L149)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### home

Return the current user's home directory.

**Signature:** `env.home() → string` · [source](https://github.com/egladman/magus/blob/main/std/env.go#L160)

**Returns:** string

### get_or

Return the value of name, or def when name is unset or stripped by the sandbox. Unlike get, an empty string is returned as-is — def only applies when the variable is absent.

**Signature:** `env.getOr(name, def) → string` · [source](https://github.com/egladman/magus/blob/main/std/env.go#L171)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |
| `def` | `string` |  | |

**Returns:** string

### require

Return the value of name, or raise when it is unset or stripped by the sandbox. The fail-fast complement to get/lookup: a CI magusfile that needs GITHUB_TOKEN states the requirement once instead of threading a lookup-then-fatal check through every caller. A set-but-empty variable satisfies the requirement (its empty value is returned).

**Signature:** `env.require(name) → string` · [source](https://github.com/egladman/magus/blob/main/std/env.go#L186)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |

**Returns:** string

[^buzz-stdlib-env-get]: `env.get` is also in Buzz's standard library (`os.env`); the magus form is sandbox-aware.
[^buzz-stdlib-env-lookup]: `env.lookup` is also in Buzz's standard library (`os.env (returns null when unset)`); the magus form is sandbox-aware.
