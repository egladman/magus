---
title: No silent defaults
order: 6
description: A default that changes what happens without saying so is a bug; misconfiguration is an error, never a warning.
tags: [design, judgment, configuration, errors]
---

# No silent defaults

A default that changes what happens without saying so is a bug. Misconfiguration
is an error, never a warning. Anything a caller must act on is a returned error,
never a notice or a log line. A fallback says that it fell back. A capability
that is absent, an engine that is not linked or a file that cannot be read,
fails loudly and never answers empty.

A setting magus cannot honor fails the command that read it. It is not logged
and stepped over, not repaired with a default, not carried as a warning nobody
reads in a CI log that scrolled past an hour ago.

The reason is what a warning communicates. You wrote the setting, so
you believe it is in effect; a warning leaves that belief standing while the
behavior underneath it is something else. Every subsequent decision rests on
a premise the tool already knew was false, and the failure surfaces somewhere
far away, as something else, long after the edit that caused it. A tool that
knows a value is wrong and proceeds anyway has chosen to be misleading.

So the test for any warning is one question: is the value honored? If it is
not, the warning is a bug in disguise, and the fix is to fail. If it is, say
nothing, because a warning about something that works is noise that teaches
people to skim past the warnings that matter.

The corollary binds the other direction: a knob magus accepts is a knob magus
must obey. A field that reads as a retention window while the code enforces a
fixed cap, or a flag documented as repeatable that silently keeps the last
value, is the same defect as the ignored setting. It just fails later, and
reads as a lie rather than an oversight.

An empty answer is the quietest default. A lookup that returns nothing because
the engine behind it was never linked, or because a file could not be read,
tells the caller there is nothing to find, and the caller believes it. Absence
of the capability is its own error, distinct from absence of a result.

## Enforced by

- `magus.yaml` decodes with unknown keys rejected (`decodeFile` in
  [`internal/config/load.go`](https://github.com/egladman/magus/blob/main/internal/config/load.go)), so a misspelled key fails the load.
- A workspace with a magusfile and no linked engine fails with
  [MGS1104](../../reference/codes/capability/MGS1104.md).
- A lookup the index cannot vouch for answers "unknown, not absent".

## Good

From [`internal/config/load.go`](https://github.com/egladman/magus/blob/main/internal/config/load.go): the unknown key fails, because a value the tool
drops must never look like one it applied.

```go
dec := yaml.NewDecoder(bytes.NewReader(data))
dec.KnownFields(true)
```

## Bad

```go
if err := yaml.Unmarshal(data, &cfg); err != nil {
    log.Printf("warning: bad config, using defaults: %v", err)
    cfg = Default()
}
```

The person who wrote the setting believes it is in effect. The log line
scrolls past, and the build behaves as something nobody configured.

## See also

- [Told, never guessed](told-never-guessed.md)
- [A refusal carries its reason](a-refusal-carries-its-reason.md)
- [Behavior is code, facts are data](../principles/behavior-is-code-facts-are-data.md)
