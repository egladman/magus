---
title: No news is good news
order: 29
description: A passing run says almost nothing; output is spent on what failed, what changed and what to do next, because every line costs a reader's attention and an agent's context.
tags: [design, principle, cli, output]
---

# No news is good news

Unix settled this decades ago: a command that worked says nothing, and the exit
status carries the verdict. magus keeps that, and it matters more now. A person
skims past a wall of green and misses the one red line. An agent pays for every
line in its context on every call after it, so a chatty pass is a tax on the
whole session.

So a quiet run prints its address and stops. With `-s`, a pass is one `ref`
line; a failure adds the diagnostics, a bounded tail of the log, and the path to
the rest. The full log is never lost, it is one `magus query output <ref>` away,
so there is no reason to print it in case someone wants it. A warning about
something that works is noise
([No silent defaults](../judgment/no-silent-defaults.md)), and a message that
only says a thing failed has wasted the one moment it had the reader's
attention ([A refusal carries its reason](../judgment/a-refusal-carries-its-reason.md)).

Quiet is not the same as hidden. Asking for more is always one flag away
(`-v`, `-vv`, `-o json`), and the guard steers an agent to `-s` rather than to a
pipe through `tail`, which would hide the exit status along with the noise.

## Enforced by

- `-s` stays quiet until something fails, then prints the diagnostics and the
  log path.
- Every run persists its full log behind its ref, so nothing printed is the only
  copy.
- The guard denies piping magus output and names `-s` or `-o` instead.

## Good

A pass, in full:

```text
ref  out2ad91ba6e6f7
```

## Bad

```text
[INFO] Resolving workspace...
[INFO] Found 214 projects
[INFO] Hashing inputs for docs (1/214)
...
[INFO] Done in 4.2s. 214 succeeded, 0 failed.
```

Two hundred lines to say nothing went wrong. A person stops reading output like
this, and an agent carries every line of it to the end of the session.

## See also

- [Every answer has an address](every-answer-has-an-address.md)
- [Unix survives at the verb, not the binary](unix-survives-at-the-verb.md)
- [A refusal carries its reason](../judgment/a-refusal-carries-its-reason.md)
