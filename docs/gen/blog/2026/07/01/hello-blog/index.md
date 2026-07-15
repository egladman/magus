---
title: Hello, blog
description: A first post to seed the magus docs blog. New here? Start with the Documentation.
tags: [meta, announcement]
date: 2026-07-01
author: Eli Gladman
---

# Hello, blog

This is the first post on the magus docs blog. Posts land here alongside the
docs, share the same rendering pipeline, and appear in the site search index.

New to magus? Start with the [documentation](../../documentation/), or try it
live in the [playground](../../playground.html).

## Try a runnable snippet

Click **Run** to execute this Buzz snippet in your browser. The WebAssembly
interpreter is fetched lazily on the first click, so unopened pages stay light:

<!-- run -->

```buzz
import "std";
final greeting = "hello from a runnable snippet";
std.print(greeting);
std.print("length is {greeting.len()}");
```

"Open in Playground →" carries the same snippet into `/playground.html` via a
`#source=<base64url>` deep-link.
