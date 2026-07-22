---
title: "MGS4005: environmental drift"
description: A generated output differs on regeneration but its declared inputs are unchanged and a dev build produced it, so the difference is toolchain or version skew, not a source change.
tags:
  [MGS4005, drift, generated, determinism, environment, version skew, toolchain]
---

# MGS4005: environmental drift

A generated output changed when you regenerated it, but its declared inputs are
byte-identical to what is committed, and you are running a dev build. The committed
form was produced by the pinned release (the compatibility contract: CI regenerates
with a checksum-verified release binary), so a dev build rendering it differently is
version or toolchain skew, not a change you made.

```text
[MGS4005] generated output drifted but its declared inputs are unchanged; the committed
form is produced by the pinned release and you are running a dev build (v0.1.0-5-gabc123)
- not your change, do not commit
```

## Why

magus treats a generated file as a pure function of its declared inputs plus the
generator and tool versions. When the inputs are unchanged, the only remaining variable
is the generator/tool version. A local HEAD build, a locally installed formatter
(prettier, a badge renderer), or any tool whose version differs from the pinned release
can render the same inputs to different bytes - the classic markdown-emphasis case
(`*x*` vs `_x_`). That difference is real, but it is not your change, and committing it
would just re-introduce the same skew against the next machine.

## Resolution

Do not commit the drift. Discard it (`git checkout -- <paths>`) and move on. If the
files genuinely need regenerating for a real reason, regenerate with the pinned release
(the version CI uses) so the bytes match the contract, not with a local dev build.

If you believe the inputs really did change, see [MGS4006](MGS4006.md) (stale generated
output) - that is the code you would get instead.
