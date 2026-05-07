# `http`

HTTP client with automatic retry on transient errors.

> **Naming convention:** Teal/Lua binds each module per-import in `snake_case` (`local http = require("magus.extra.http")`, then `http.some_method`). Buzz reaches them off the `import "magus/extra"` aggregate in `camelCase` (`extra.http.someMethod`).

## Methods

### `get`

Send a GET request; returns (status_code, body).

**Signature (Teal):** `http.get(url, [headers]) → status, body`

**Signature (Buzz):** `extra.http.get(url, [headers]) → status, body`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `url` | `string` |  | |
| `headers` | `map[string]string` | yes | |

**Returns:** status int, body string

### `post`

Send a POST request with body; returns (status_code, body).

**Signature (Teal):** `http.post(url, body, [headers]) → status, body`

**Signature (Buzz):** `extra.http.post(url, body, [headers]) → status, body`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `url` | `string` |  | |
| `body` | `string` |  | |
| `headers` | `map[string]string` | yes | |

**Returns:** status int, body string

### `request`

Send an HTTP request; returns (status_code, body).

**Signature (Teal):** `http.request(method, url, [body], [headers]) → status, body`

**Signature (Buzz):** `extra.http.request(method, url, [body], [headers]) → status, body`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `method` | `string` |  | |
| `url` | `string` |  | |
| `body` | `string` | yes | |
| `headers` | `map[string]string` | yes | |

**Returns:** status int, body string

