---
title: env module
aliases: [modules/env]
description: Process environment variable access.
tags: [env, module, stdlib, magusfile]
---

# env

Process environment variable access.

> **Naming convention:** import the module under its bare name (`import "env"`) and call methods in `camelCase` (`env.someMethod`).

## Methods

### get

Return the value of name, or "" if unset. Use lookup to tell unset from set-but-empty.

**Signature:** `env.get(name) → string`[^buzz-stdlib-env-get] · [source](https://github.com/egladman/magus/blob/main/std/env.go#L111)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |

**Returns:** string

### lookup

Return (value, found); found is false when name is unset or stripped by the sandbox.

**Signature:** `env.lookup(name) → string, bool`[^buzz-stdlib-env-lookup] · [source](https://github.com/egladman/magus/blob/main/std/env.go#L126)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |

**Returns:** string, bool

### set

Set name to value in the current process environment.

**Signature:** `env.set(name, value)` · [source](https://github.com/egladman/magus/blob/main/std/env.go#L135)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |
| `value` | `string` |  | |

### list

Return all environment variables as a name→value map.

**Signature:** `env.list() → map[string]string` · [source](https://github.com/egladman/magus/blob/main/std/env.go#L216)

**Returns:** map[string]string

### unset

Remove name from the current process environment.

**Signature:** `env.unset(name)` · [source](https://github.com/egladman/magus/blob/main/std/env.go#L152)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |

### expand

Replace $VAR and ${VAR} references in s with their values (sandbox-stripped names expand to "").

**Signature:** `env.expand(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/env.go#L167)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### home

Return the current user's home directory.

**Signature:** `env.home() → string` · [source](https://github.com/egladman/magus/blob/main/std/env.go#L178)

**Returns:** string

### get_or

Return the value of name, or def when name is unset or stripped by the sandbox. Unlike get, an empty string is returned as-is — def only applies when the variable is absent.

**Signature:** `env.getOr(name, def) → string` · [source](https://github.com/egladman/magus/blob/main/std/env.go#L189)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |
| `def` | `string` |  | |

**Returns:** string

### require

Return the value of name, or raise when it is unset or stripped by the sandbox. The fail-fast complement to get/lookup: a CI magusfile that needs GITHUB_TOKEN states the requirement once instead of threading a lookup-then-fatal check through every caller. A set-but-empty variable satisfies the requirement (its empty value is returned).

**Signature:** `env.require(name) → string` · [source](https://github.com/egladman/magus/blob/main/std/env.go#L204)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |

**Returns:** string

### parse_dotenv

Parse .env-format content into a name->value map. Supports KEY=VALUE, blank lines, # comments, a leading `export` keyword, single/double quotes (double-quoted values honor \n \t \" \\ escapes), and inline comments after unquoted values. Pure: it does not touch the process environment.

**Signature:** `env.parseDotenv(content) → map[string]string` · [source](https://github.com/egladman/magus/blob/main/std/env.go#L237)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `content` | `string` |  | |

**Returns:** map[string]string

### read_dotenv

Read a .env file and return its name->value map (parse_dotenv over the file contents). Errors if the file cannot be read.

**Signature:** `env.readDotenv(path) → map[string]string` · [source](https://github.com/egladman/magus/blob/main/std/env.go#L242)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** map[string]string

### load_dotenv

Read a .env file and set each variable in the process environment, without overwriting names already set (the dotenv convention) or names the sandbox strips. A no-op in a recording/dry-run.

**Signature:** `env.loadDotenv(path)` · [source](https://github.com/egladman/magus/blob/main/std/env.go#L253)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

[^buzz-stdlib-env-get]: `env.get` is also in Buzz's standard library (`os.env`); the magus form is sandbox-aware.
[^buzz-stdlib-env-lookup]: `env.lookup` is also in Buzz's standard library (`os.env (returns null when unset)`); the magus form is sandbox-aware.
