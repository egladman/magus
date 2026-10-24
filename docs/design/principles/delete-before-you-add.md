---
title: Delete before you add
order: 18
description: The best code is still no code; before adding code to keep something working, check whether removing the old path reaches the goal.
tags: [design, principle, scope, maintenance]
---

# Delete before you add

Every line you ship is a bill somebody else pays: the person installing the
tool, or whoever maintains it after you. It almost never lands on the person
who wrote it, and that is most of why we keep adding. Jeff Atwood said it in
2007 and it has only gotten truer: the best code is no code at all.[^atwood]
Writing a feature is close to free now, so the only thing between a codebase
and endless creep is somebody deciding not to add one, or deciding to take one
out.

People are bad at this in a measurable way. Adams and colleagues found that
people handed a problem reach for what they can add and barely consider what
they could take away, even where removing something is the better
answer.[^subtract] Deleting a feature reads as a loss. It is usually a gift to
whoever maintains the thing next, and agents drift the same way: asked to make
something work, they add a fallback, a flag, a shim.

Removal is always an option, for whole features too. A feature in magus has to
be done really well. If it is half-built, or another tool does the job better
and magus cannot honestly beat it, the answer can be to say so, remove it, and
point people at the tool that does it well. Building it out further is the
other answer; leaving it half-done is not one.

magus is pre-1.0, so deleting is cheap now and gets expensive later. A rename,
a removed flag or a changed format ships without a migration path; a shim needs
my yes and carries the condition that retires it
([Break now, hold still later](break-now-hold-still-later.md)).

## Enforced by

- `AGENTS.md` makes deletion the first option: before adding code to keep
  something working, check whether removing the old path, flag, fallback, test
  or doc section reaches the goal.
- An approved shim carries `compat(until: <condition>):`, a condition someone
  can observe, so it has a way out.
- [A record of refusals](../refusals.md) keeps the deletions that were measured,
  so a removed thing is not quietly re-added.

## Good

A shim that knows how it ends:

```go
// compat(until: no store still serves ed25519 envelopes): read the old envelope too.
```

## Bad

```go
func load(path string) (*Config, error) {
    cfg, err := loadV2(path)
    if err != nil {
        return loadV1(path) // fall back in case an old file is still around
    }
    return cfg, nil
}
```

Two readers now live forever, nobody can say when the second one is safe to
delete, and a broken new file reads as an old one in silence.

## See also

- [Capability is not a reason](capability-is-not-a-reason.md)
- [Clear is better than clever](clear-is-better-than-clever.md)
- [Break now, hold still later](break-now-hold-still-later.md)

[^atwood]: Jeff Atwood, [The Best Code is No Code At
    All](https://blog.codinghorror.com/the-best-code-is-no-code-at-all/), 2007.

[^subtract]: Adams, Converse, Hales and Klotz, [People systematically overlook
    subtractive changes](https://www.nature.com/articles/s41586-021-03380-y),
    Nature 592, 2021.
