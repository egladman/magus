# `http`

HTTP client with automatic retry on transient errors.

> **Naming convention:** import the module under its bare name (`import "http"`) and call methods in `camelCase` (`http.someMethod`).

## Methods

### `get`

Send a GET request; returns {status, body, headers}. opts (curl-style): fail, fail_with_body, fail_early (bool); retry (int), retry_delay, retry_max_time, timeout (seconds, default 30); retry_all_errors, retry_connrefused (bool).

**Signature:** `http.get(url, [headers], [opts]) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `url` | `string` |  | |
| `headers` | `map[string]string` | yes | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### `post`

Send a POST request with body; returns {status, body, headers}. opts (curl-style): fail, fail_with_body, fail_early (bool); retry (int), retry_delay, retry_max_time, timeout (seconds, default 30); retry_all_errors, retry_connrefused (bool).

**Signature:** `http.post(url, body, [headers], [opts]) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `url` | `string` |  | |
| `body` | `string` |  | |
| `headers` | `map[string]string` | yes | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### `request`

Send an HTTP request; returns {status, body, headers}. opts (curl-style): fail, fail_with_body, fail_early (bool); retry (int), retry_delay, retry_max_time, timeout (seconds, default 30); retry_all_errors, retry_connrefused (bool).

**Signature:** `http.request(method, url, [body], [headers], [opts]) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `method` | `string` |  | |
| `url` | `string` |  | |
| `body` | `string` | yes | |
| `headers` | `map[string]string` | yes | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

