---
title: http module
description: HTTP client with automatic retry on transient errors. Get, post, and generic request methods plus a background static file server for local dev.
tags: [http, http client, get, post, request, retry, static server, magus stdlib]
---

# http

HTTP client with automatic retry on transient errors.

> **Naming convention:** import the module under its bare name (`import "http"`) and call methods in `camelCase` (`http.someMethod`).

## Methods

### get

Send a GET request; returns {status, body, headers}. opts (curl-style): fail, fail_with_body, fail_early (bool); retry (int), retry_delay, retry_max_time, timeout (seconds, default 30); retry_all_errors, retry_connrefused (bool).

**Signature:** `http.get(url, [headers], [opts]) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/http.go#L106)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `url` | `string` |  | |
| `headers` | `map[string]string` | yes | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### post

Send a POST request with body; returns {status, body, headers}. opts (curl-style): fail, fail_with_body, fail_early (bool); retry (int), retry_delay, retry_max_time, timeout (seconds, default 30); retry_all_errors, retry_connrefused (bool).

**Signature:** `http.post(url, body, [headers], [opts]) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/http.go#L112)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `url` | `string` |  | |
| `body` | `string` |  | |
| `headers` | `map[string]string` | yes | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### request

Send an HTTP request; returns {status, body, headers}. opts (curl-style): fail, fail_with_body, fail_early (bool); retry (int), retry_delay, retry_max_time, timeout (seconds, default 30); retry_all_errors, retry_connrefused (bool).

**Signature:** `http.request(method, url, [body], [headers], [opts]) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/http.go#L118)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `method` | `string` |  | |
| `url` | `string` |  | |
| `body` | `string` | yes | |
| `headers` | `map[string]string` | yes | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### server

Start a static file server for dir in the background and return the bound port. With no port (or 0) it scans upward from 8080 and binds the first available port. Serves localhost only and runs until the process exits, so pair it with a blocking call like fs.watch.

**Signature:** `http.server(dir, [port]) → int` · [source](https://github.com/egladman/magus/blob/main/std/http.go#L127)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `dir` | `string` |  | |
| `port` | `int` | yes | |

**Returns:** int

