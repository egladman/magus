---
title: "I didn't set out to build a harness"
description: "magus started as a monorepo tool I didn't want anywhere near agents. Then I realized it had become a harness anyone can import from Go, and that starting at the tool layer is why it sits at the right layer."
tags: [opinion]
date: 2026-09-14
draft: true
---

# I didn't set out to build a harness

How magus started is different from a lot of the tools in this space. Most of
the agentic tools started off as agentic tools. This one didn't. I was building
a monorepo tool, a build tool for people, and I didn't want it anywhere near
associated with agents. It just naturally started to coexist with them. The
stars aligned.

At some point I took a step back and thought: this looks just like what I've
done with Mastra at work, but better. Then it clicked. I had built a harness by
accident, and not one just for me: one anybody can import from Go.

## The right layer is boring

A task runner already has to know what every task reads and writes. magus keys
its cache on that, its graph knows what a diff reaches, and the guard and job
leases sit on the same record. The CLI and the harness are both the `magus`
binary, built on the Go SDK. It's extendable through Buzz, and some of the main
default integrations are Buzz spells: anybody can copy one, import their own
local file and do whatever they want with it. They're not dependent on us.

<!--diagram:harness-layer-->

Starting there is what makes it the right layer. Most agent frameworks started
from the model instead. They're reactionary and too tight: they ship in lockstep
with new models and they're always playing catch up, because a frontier model
is a moving target. Superpowers is the one I keep bumping into: a methodology
injected at session start, skills that insist on being loaded, subagent
choreography, review loops. It does way too much, and at a certain point you're
competing against your own model. The only realistic thing is to loosen the
reins, but at the right layers.

Here's the advanced example from [the Go SDK guide](../../guides/go-sdk/).
The program is its own harness: the guard judges each command the model
proposed, the build runs with its declared inputs and outputs, and the second
run is a cache hit.

```go
reg := magus.NewWorkspaceRegistry()
reg.RegisterProject("api", magus.WithSpell("guide-harness"))

ctx := context.Background()
m, err := magus.Open(ctx, root, magus.WithWorkspaceRegistry(reg), magus.WithSpawnRule(magus.TierCeiling))
if err != nil {
	fmt.Println("open:", err)
	return
}
defer m.Close()

// What a model asked to run, in order. A real harness reads these from its agent loop.
proposed := []string{"cp api/greeting.txt api/out/", "magus run build api", "magus run build api"}
for _, command := range proposed {
	j, err := m.JudgeCommand(ctx, "guide-session", root, command)
	if err != nil {
		fmt.Println("judge:", err)
		return
	}
	if j.Decision == "deny" {
		reason, _, _ := strings.Cut(j.Reason, "\n")
		fmt.Printf("denied %q: %s\n", command, reason)
		continue
	}
	target, ok := strings.CutPrefix(command, "magus run ")
	name, path, _ := strings.Cut(target, " ")
	if !ok {
		fmt.Printf("allowed %q, which this harness does not run\n", command)
		continue
	}
	results, err := runRecorded(ctx, m, []types.Target{{Path: path, Name: name}})
	if err != nil {
		fmt.Println("run:", err)
		return
	}
	for _, r := range results {
		fmt.Printf("ran %s:%s: %s, cache hit %v, builds so far %d\n", r.Project, r.Target, r.Status, r.CacheHit, runs)
	}
}
// Output:
// denied "cp api/greeting.txt api/out/": Build through the target: magus run build api
// ran api:build: ok, cache hit false, builds so far 1
// ran api:build: ok, cache hit true, builds so far 1
```

## What a human trips on, an agent trips on

It's all about explicitness. You need good errors that are actionable and show
the good and the bad. You don't need a RAG model for most of this; there are
use cases for it, but at a lot of people's scale it isn't necessary.

My hunch is that this is the same explicitness you'd train into a model. I
can't prove that part. What I can point to is Anthropic's guidance on writing
tools for agents, which says to "prompt-engineer your error responses to
clearly communicate specific and actionable improvements" and to show correctly
formatted input.[^tools] That's explicitness in the context, with no training
run.

I understand that my agents are just markdown. Stop writing markdown in code.
Caveman compresses prose, which is too deep in it: the model says exactly what
it wants to say. Don't compress prose. Don't write prose. Ponytail pushes YAGNI
into the code the agent writes, and I like that part, the concept of proving
it. i-have-adhd reshapes answers so a human can scan them, which is about the
output, not the input, so it isn't the same kind of thing.

## Determinism where it counts

Add intentional layers of determinism, placed strategically, then let the model
do its job. Lock it down too much and you're fighting your own model. There's a
level of nondeterminism you have to accept. Overfitting is a training word, but
I think it applies past training: you can't evaluate every little thing, and
most of the time a human is the one putting input in. A harness needs to be a
guiding hand that reinforces, enforces, and can block.

On types, a middle ground. Models don't inherently know JSON; it's been hacked
together to work. In my experience it's easy for an agent to output structured
data and harder for it to take structured data in. I've written a feature for
that and haven't turned it on yet.

## What I compared it to

My firsthand experience is Mastra, and I don't like it. It tries to do too
much. It wires up the LLM providers for you,[^mastra] and that seam belongs to
the developer implementing it. Things move so quickly that a framework owning
it is bound to break things or overextend itself.

I don't agree with Pi. Its default tools are read, bash, edit and
write,[^pi-tools] and its bash tool hands the model back one string of combined
stdout and stderr.[^pi-bash] It needs more than that, and bash has no place
here: it should be structured data only. I say that as a bash elitist who once
wrote a TOML parser in pure bash, without even coreutils.

DeepSeek Harness is TypeScript, about 4,400 `.ts` files across its packages,
built on Cordis, where every part, the agent loop included, is a plugin.[^dsh]
Its tools declare an output schema, and a result that breaks it is
rejected.[^dsh-tools] An optional policy refuses to edit a file the agent
hasn't read, or that changed since.[^dsh-fs] Those are the kind of guards I
mean. Its base bundle mounts a bash tool too. From the outside looking in it
still reads as over-engineered to me, but I don't know enough about it to say
more.

[^tools]: Ken Aizawa, [Writing effective tools for agents](https://www.anthropic.com/engineering/writing-tools-for-agents),
    Anthropic, 2025-09-11.

[^mastra]: Mastra documentation, [Overview](https://mastra.ai/docs).

[^pi-tools]: `DEFAULT_TOOL_NAMES` in [settings-manager.ts](https://github.com/earendil-works/pi/blob/6c2fd73b0c545a4034672cb60e34d53e035dd751/packages/coding-agent/src/core/settings-manager.ts#L85).

[^pi-bash]: `bashOutputSchema` in [bash.ts](https://github.com/earendil-works/pi/blob/6c2fd73b0c545a4034672cb60e34d53e035dd751/packages/coding-agent/src/core/tools/bash.ts#L56).

[^dsh]: [deepseek-ai/deepseek-harness](https://github.com/deepseek-ai/deepseek-harness/tree/22ddee3792402c3e70ec5f86a63c3e3e895a2f60)
    at 22ddee37, 2026-10-23: 4,414 `.ts` files under `packages/`, and
    [docs/architecture.md](https://github.com/deepseek-ai/deepseek-harness/blob/22ddee3792402c3e70ec5f86a63c3e3e895a2f60/docs/architecture.md).

[^dsh-tools]: [core/tools/src/pipeline.ts](https://github.com/deepseek-ai/deepseek-harness/blob/22ddee3792402c3e70ec5f86a63c3e3e895a2f60/packages/core/tools/src/pipeline.ts#L540),
    which throws `ToolOutputError` (`INVALID_TOOL_OUTPUT`).

[^dsh-fs]: [fs/fs-observation-policy](https://github.com/deepseek-ai/deepseek-harness/tree/22ddee3792402c3e70ec5f86a63c3e3e895a2f60/packages/fs/fs-observation-policy).
