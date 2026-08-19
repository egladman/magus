---
title: "The console was an experiment"
description: "Cut from the tools post. magus ships a web console I built to test my own prejudice about GUIs, and I owed the question a verdict before writing about it."
tags: [opinion]
date: 2026-09-08
draft: true
---

# The console was an experiment

<!-- Lifted verbatim from the 2026-08-18 tools post, where it landed without a
verdict. Hold it until there is one: whether it surfaces something the CLI cannot,
or whether it goes. -->

magus ships a web console, which could fairly be read as feature creep, and I am not
going to argue that it obviously is not. I built it as an experiment, to find out
whether I get genuine value out of having one, and I have not reached a verdict. I am
still using it, still refining it, and still deciding. Do not read this as me having
quietly concluded no.

I am usually the one giving other people grief about GUIs built on web technology, so
read this as me testing my own prejudice. What I want to know is whether it can get
where I want it on performance, and the only way to find that out was to build enough
of it to judge.

Shipping it decoupled is what keeps the experiment cheap. It is a static build in its
own project, and every contract between it and the daemon is declared in protobuf, so
the surface it leans on can stay stable without me breaking it later. Nothing in the
CLI depends on it. It costs me very little to try and very little to delete, which is
the only reason I was willing to find out rather than keep arguing with myself about
it.

The bar is whether it tells me something I cannot already get. A graph I query from the
terminal, redrawn as a graph I can click, is not automatically worth maintaining.
Plenty of tools already draw boxes. If it does not surface a data point the CLI cannot,
or make one materially faster to reach, then it goes, and I will find that out by
whether I keep opening it.

This is also where I have found the AI tooling useful, and I want to be specific
about it, having spent a fair amount of ink elsewhere complaining. The cost of building
something well enough to judge it used to be high enough that experiments like this
died as ideas. Now I can run the experiment and answer whether it becomes sticky with
a real thing in my hands instead of a guess.

