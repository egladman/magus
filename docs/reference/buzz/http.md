---
title: http module
aliases: [modules/http]
description: HTTP client.
tags: [http, module, stdlib, magusfile]
---

# http

HTTP client. Requests run ONCE unless given a retry policy.

> **Naming convention:** import the module under its bare name (`import "http"`), reach members with a backslash, and call methods in `camelCase`: `http\someMethod`.

<!-- -->

> [!NOTE]
> The examples below are reference-only. `http` performs real IO (filesystem, process, network, or environment access) that the in-browser playground's sandbox cannot provide, so it is not registered there and its examples have no Run button. Pure-compute modules such as `strings` and `json` run their examples live in the page.

## Methods

### get

Send a GET request; returns {status, body, headers}. opts (curl-style): fail, fail_with_body, fail_early (bool); timeout (seconds, default 30). Retrying is NOT configured here - pass a typed HttpRetry as the retry argument; without one the request runs exactly once.

**Signature:** `http\get(url, [headers], [opts], [retry]) → HttpResponse` · [source](https://github.com/egladman/magus/blob/main/std/http.go#L140)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `url` | `string` |  | |
| `headers` | `map[string]string` | yes | |
| `opts` | `map[string]any` | yes | |
| `retry` | `map[string]any` | yes | |

**Returns:** map[string]any

**Example:**

```buzz
import "std";
import "http";

final r = http\get("https://api.github.com/repos/egladman/magus");
std\print(r.status);
std\print(r.body.sub(0, 80) + "...");
```

### download

GET url and stream the response body straight to dest, returning the HTTP status. The body never becomes a Buzz string, so arbitrary binary (a release tarball, an image layer) survives intact and a large file costs no proportional memory. A non-2xx status writes nothing. Pair it with crypto.sha256_file to verify what you fetched before using it. opts (curl-style): fail, fail_with_body, fail_early (bool); timeout (seconds, default 30). Retrying is NOT configured here - pass a typed HttpRetry as the retry argument; without one the request runs exactly once.

**Signature:** `http\download(url, dest, [headers], [opts], [retry]) → int` · [source](https://github.com/egladman/magus/blob/main/std/http.go#L154)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `url` | `string` |  | |
| `dest` | `string` |  | |
| `headers` | `map[string]string` | yes | |
| `opts` | `map[string]any` | yes | |
| `retry` | `map[string]any` | yes | |

**Returns:** int

### post

Send a POST request with body; returns {status, body, headers}. opts (curl-style): fail, fail_with_body, fail_early (bool); timeout (seconds, default 30). Retrying is NOT configured here - pass a typed HttpRetry as the retry argument; without one the request runs exactly once.

**Signature:** `http\post(url, body, [headers], [opts], [retry]) → HttpResponse` · [source](https://github.com/egladman/magus/blob/main/std/http.go#L215)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `url` | `string` |  | |
| `body` | `string` |  | |
| `headers` | `map[string]string` | yes | |
| `opts` | `map[string]any` | yes | |
| `retry` | `map[string]any` | yes | |

**Returns:** map[string]any

**Example:**

```buzz
import "http";

// Post JSON. opts carries the curl-style settings; retrying is a separate,
// typed HttpRetry argument, and without one the request runs exactly once.
// Escape { and } as \{ \} so Buzz does not try to interpolate them.
final r = http\post(
    "https://httpbin.org/post",
    "\{\"target\":\"build\"\}",
    {"Content-Type": "application/json"},
    {"timeout": 10},
    http\HttpRetry{ attempts = 3, delay_ms = 500.0 },
);
```

### request

Send an HTTP request; returns {status, body, headers}. opts (curl-style): fail, fail_with_body, fail_early (bool); timeout (seconds, default 30). Retrying is NOT configured here - pass a typed HttpRetry as the retry argument; without one the request runs exactly once.

**Signature:** `http\request(method, url, [body], [headers], [opts], [retry]) → HttpResponse` · [source](https://github.com/egladman/magus/blob/main/std/http.go#L221)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `method` | `string` |  | |
| `url` | `string` |  | |
| `body` | `string` | yes | |
| `headers` | `map[string]string` | yes | |
| `opts` | `map[string]any` | yes | |
| `retry` | `map[string]any` | yes | |

**Returns:** map[string]any

**Example:**

```buzz
import "http";

// request lets you pick any method; useful for PUT/PATCH/DELETE.
final r = http\request(
    "PUT",
    "https://httpbin.org/put",
    "hello",
    { "Content-Type": "text/plain" },
    { "timeout": 10 },
);
```

### server

Start a static file server in the background from an options map and return the bound port. opts keys: dir (string) serves a single directory; OR mounts (a map of URL-prefix -> dir, e.g. {"/": "docs/gen", "/console/": "console/gen"}) serves multiple roots where a request routes to the LONGEST matching prefix, so "/console/" wins over "/" for a /console/ path and the matched prefix is stripped before the file lookup. Exactly one of dir or mounts is required. port (int, optional) binds that port; 0 (the default) scans upward from 8080 and binds the first available one. Unknown keys are rejected. Serves localhost only and runs until the process exits, so pair it with a blocking call like fs.watch.

**Signature:** `http\server(opts) → int` · [source](https://github.com/egladman/magus/blob/main/std/http.go#L233)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `opts` | `map[string]any` |  | |

**Returns:** int

**Example:**

```buzz
import "http";

// Serve the current build output over http on port 8080 for quick sharing.
// Blocks until the process exits.
http\server({"dir": "dist/", "port": 8080});
```

