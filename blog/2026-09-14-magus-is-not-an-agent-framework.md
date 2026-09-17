---
title: "magus is not an agent framework"
description: "Why I am not shipping an agent framework, and why Claude Code, Codex, Cursor, and OpenCode stay out of the magus binary."
tags: [opinion]
date: 2026-09-14
draft: true
---

# magus is not an agent framework

Most of the frameworks and skill packs people bolt onto coding agents right now
feel like they are solving last season's model. Superpowers is the one I keep
bumping into: a methodology injected at session start, skills that insist on
being loaded, subagent choreography, review loops. Six months ago that shape may
have propped up a weaker model. On the ones I am using now I keep finding it
gets in the way. The model already plans. It already checks itself. The
framework still spends tokens telling it how to be careful.

The smaller packs are a quieter version of the same thing. Caveman compresses
prose. Ponytail pushes YAGNI into the code the agent writes. i-have-adhd reshapes
answers so a human can scan them. Some of that is still useful if you are on a
weaker model, or you want a particular shape for yourself rather than for the
model. An always-on methodology that fires whether you asked or not is expensive
in ways a feature list will not show you.

What bothers me more than the token bill is that these things are point-in-time
claims about a system that is not standing still. A trick that worked in March
is not a library with a stable API. The providers keep improving the harnesses
you never see, and anything you wrap around those models ends up chasing the
last release. I do not want to ship that. I said something close to this in the
tools post: I am skeptical of the agent-first toolkit category, I think most of
it is gone in two years, and I am not adding to it.

So the bar I keep for anything that sits near a model is that the next release
does not have to fight it. If it degrades the next one, it was never a
foundation.

## Where magus sits

magus is a build tool for people. Agents happen to drive it because people do.

Nowhere in the magus codebase do we hardcode Claude Code, Codex, Cursor, or
OpenCode. Host-specific data lives in descriptors and in glue you own. A fifth
host is a JSON file and maybe a shell script, not a magus release. That is the
trade: you keep a little wiring in the repo, and we do not force every checkout
to wait on us every time somebody else's agent host moves.

If the glue broke and the only paths forward were a crude bypass or waiting on a
tag that names your host, I would expect people to leave. Host independence is
how we keep a path open without pretending magus is the agent runtime.
