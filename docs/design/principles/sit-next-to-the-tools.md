---
title: Sit next to the tools, never in front of them
order: 20
description: A build tool runs other tools; it stays thin enough that you can always see which command ran and reach the tool directly.
tags: [design, principle, spells, abstraction]
---

# Sit next to the tools, never in front of them

A build tool is in an odd meta position: its whole job is running other tools.
The most useful thing it can do is stay out of the space between you and them.
You should be able to see which command runs and how its arguments were
assembled, because software breaks in ways nobody designed for, and at that
moment the only thing that matters is whether you can work out what happened.
Every layer between you and the actual command is a layer you peel back first,
usually while something is on fire.

Engineers are drawn to abstracting things away, and it is usually the right
instinct. So one team wraps a tool, another wraps that wrapper, and a third
builds on the second. Each layer looks reasonable to whoever wrote it. Whoever
writes layer three can see layer two; they cannot see the tool at the bottom,
its failure modes, or which of its features layer two quietly dropped, so they
abstract over a model of the thing rather than the thing. Skaffold is where I
feel this most: magic for a week, then you need something its authors did not
picture and there is no seam, because the machinery you need to reason about
is the machinery it exists to hide. Temporal says the opposite out loud: a thin
shim is fine, a friendly layer is fine, as long as people can still reach the
SDK directly.

The tightly built wrapper has a social cost too. Someone writes an executor
around one job, it works, and when it breaks everyone goes through that one
person, because they are the only one who can see what it does. That is a bus
factor of one, manufactured by the design.

## Enforced by

- A spell is a library of tool-native ops: its op runs the tool with arguments
  you can read, and the run records the command it executed.
- `magus query output <ref>` prints a run's full log, the command included.
- [Automation you can interrogate](../judgment/automation-you-can-interrogate.md)
  requires each step to be expressible as the toolchain call it made.

## Good

The run names the tool and its arguments, so you can run it yourself without
magus:

```sh
magus run test . -v
magus query output <ref>
```

## Bad

```yaml
build:
  artifacts:
    - image: app
      custom:
        buildCommand: ./build.sh
```

Skaffold's escape hatch: the tool that actually builds is a script behind a
type field, behind the wrapper. When it fails, the wrapper's error is about the
wrapper.

## See also

- [Behavior is code, facts are data](behavior-is-code-facts-are-data.md)
- [No generated code in the build path](no-generated-code-in-the-build-path.md)
- [Automation you can interrogate](../judgment/automation-you-can-interrogate.md)
