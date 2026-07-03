---
title: http module
description: HTTP client with automatic retry on transient errors.
tags: [http, module, stdlib, magusfile]
---

# http

HTTP client with automatic retry on transient errors.

> **Naming convention:** import the module under its bare name (`import "http"`) and call methods in `camelCase` (`http.someMethod`).

## Methods

### get

Send a GET request; returns {status, body, headers}. opts (curl-style): fail, fail_with_body, fail_early (bool); retry (int), retry_delay, retry_max_time, timeout (seconds, default 30); retry_all_errors, retry_connrefused (bool).

**Signature:** `http.get(url, [headers], [opts]) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/http.go#L108)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `url` | `string` |  | |
| `headers` | `map[string]string` | yes | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

**Example:**

```buzz
import "std";
import "http";

final r = http.get("https://api.github.com/repos/egladman/magus");
std.print(r.status);
std.print(r.body.sub(0, 80) + "...");
```

### post

Send a POST request with body; returns {status, body, headers}. opts (curl-style): fail, fail_with_body, fail_early (bool); retry (int), retry_delay, retry_max_time, timeout (seconds, default 30); retry_all_errors, retry_connrefused (bool).

**Signature:** `http.post(url, body, [headers], [opts]) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/http.go#L114)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `url` | `string` |  | |
| `body` | `string` |  | |
| `headers` | `map[string]string` | yes | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

**Example:**

```buzz
import "http";

// Post JSON with a curl-style opts map for retry behaviour.
// Escape { and } as \{ \} so Buzz does not try to interpolate them.
final r = http.post(
    "https://httpbin.org/post",
    "\{\"target\":\"build\"\}",
    {"Content-Type": "application/json"},
    {"retry": 3, "timeout": 10},
);
```

### request

Send an HTTP request; returns {status, body, headers}. opts (curl-style): fail, fail_with_body, fail_early (bool); retry (int), retry_delay, retry_max_time, timeout (seconds, default 30); retry_all_errors, retry_connrefused (bool).

**Signature:** `http.request(method, url, [body], [headers], [opts]) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/http.go#L120)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `method` | `string` |  | |
| `url` | `string` |  | |
| `body` | `string` | yes | |
| `headers` | `map[string]string` | yes | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

**Example:**

```buzz
import "http";

// request lets you pick any method; useful for PUT/PATCH/DELETE.
final r = http.request(
    "PUT",
    "https://httpbin.org/put",
    "hello",
    { "Content-Type": "text/plain" },
    { "timeout": 10 },
);
```

### server

Start a static file server for dir in the background and return the bound port. With no port (or 0) it scans upward from 8080 and binds the first available port. Serves localhost only and runs until the process exits, so pair it with a blocking call like fs.watch.

**Signature:** `http.server(dir, [port]) → int` · [source](https://github.com/egladman/magus/blob/main/std/http.go#L129)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `dir` | `string` |  | |
| `port` | `int` | yes | |

**Returns:** int

**Example:**

```buzz
import "http";

// Serve the current build output over http on port 8080 for quick sharing.
// Blocks until the process exits.
http.server("dist/", 8080);
```

