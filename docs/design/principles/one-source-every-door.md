---
title: One source, every door
order: 23
description: A capability lives once, in the layer every caller reaches; the CLI, a magusfile, a script and the MCP tools are doors onto it, never copies of it.
tags: [design, principle, cli, mcp, buzz]
---

# One source, every door

Structured output was never an AI feature. `-o json`, `-o name`,
`-o template=` and an output ref for anything that ran are what a command line
has owed its users for forty years. Point an agent at `magus ls -o json` and it
gets what an MCP tool would have handed it. If a CLI needs a protocol server
before an agent can drive it, the problem sits upstream of the server.

So a capability lives in one place, and the CLI, the MCP tools and a script
are each a door onto it. The
Buzz module is the layer a magusfile, a `magus buzz` script and anything built
on them call into, so putting a capability there gives every caller the same
access, rather than reserving it for whichever door was built most recently.
Guidance follows the same rule: anything worth saying to an agent in a tool
description is worth saying to a person, as a hint or an error that names the
next step.

The same holds for the CLI's own description of itself. Flags, configuration
keys, help text, completions and man pages come from one generated source, so
no second parser or help printer can disagree with the first.

## Enforced by

- The man pages and the CLI reference are generated from the command registry
  (`internal/commands/registry.go`), and the drift gate fails a hand edit.
- The MCP tool catalog is generated from the same descriptor the CLI reads.
- Review: a capability only one door reaches says why.

## Good

The same answer through two doors, from one implementation:

```sh
magus ls -o json
magus buzz -e 'import "std"; import "magus"; fun main(args: [str]) > void !> any { std\print("{magus\describe.file(["MAGUS.md"]).files.len()}"); }'
```

## Bad

```text
mcp tool "list_projects_with_details": a projects listing with owners and
targets that no CLI verb returns
```

The agent's door now reaches something the person's does not. The two drift,
and the person loses the one view the tool chose to give only to the machine.

## See also

- [Human-first is the AI integration](human-first-is-the-ai-integration.md)
- [Unix survives at the verb, not the binary](unix-survives-at-the-verb.md)
