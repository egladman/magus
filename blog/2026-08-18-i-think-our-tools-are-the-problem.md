---
title: "I think our tools are the problem"
description: "Context engineering. Loop engineering. Graph engineering. New names for old work. The complaint underneath all of them is that our developer tools have slipped. Thoughts on Nx, Nix, Dagger, and what I wanted instead."
tags: [opinion]
date: 2026-08-18
draft: false
---

# I think our tools are the problem

Context engineering. Loop engineering. Graph engineering. That is roughly the order
I watched them show up, each one arriving as the name for the job, and each time the
conversation moved over to it.

I am not going to claim our tools are the whole problem. I do think they are a big
enough part of it to explain why these terms keep finding an audience. The work
underneath the labels is often real. The labels themselves are misnomers, and what
they have in common is not one shared complaint so much as one shared root: our
developer tools have slipped. They were not always this bad.

It is the XY problem at industry scale. Each term names a workaround, and none of
them names the thing that made the workaround necessary. Our tools got hard for
people to work with, which made them hard for agents to work with too, because the
agents learned from the same material we did.

The same churn produced a wave of agent-first toolkits, announced with
a paragraph of emoji and shipped without much evidence anyone thought about
quality. I am skeptical of the whole category. I think most of it is gone in two
years, and I am not adding to it.

I ended up writing my own build tool, magus, and this post is the why behind it.

One thing before I start, because a fair amount of what follows is critical and I
do not want it read as a man yelling at build tools. Every tool I name here was
built by people working on a genuinely hard problem, several of them better than I
would have. I am arguing with specific decisions rather than with the people who
made them, and I argue with those decisions because they are where magus came from.
Describing what I built without describing what pushed me to it would leave out the
honest half.

## Our tools stopped being idiomatic

Some of the tools most of us depend on took the Unix conventions and dropped
them. Decades of agreement about what a command looks like, what it prints, what
it writes to your disk, what a non-zero exit means, all traded for a house
dialect with abstractions stacked on top.

Now those same maintainers ship AI integrations and find that agents struggle.

An agent that cannot drive your CLI is telling you something about your CLI. You
had that evidence before the agents arrived, because your users were already
failing at it. They blamed themselves and read the docs again.

## The loop I keep watching

Your tool has a foot gun. You trip on it, and so does everyone on your team. One
of you writes a wrapper script, another answers a Stack Overflow (RIP) question,
a third publishes a "gotchas" post. Model builders train on all of that, so the
models learn the workaround and never see the fix. Your agent trips on the same
idiom you tripped on, having learned it from you. Somebody ships an integration
to paper over the tool.

Nothing in that circuit puts pressure on the tool itself.

## Nx

[Nx](https://nx.dev) pushed me into writing my own build tool. I want this aimed at decisions
rather than at the people who made them, and I would rather have the argument
with those people than about them.

Run `nx --help` and count. Then try to say out loud what rule decides whether
something is a subcommand, a flag, or a target in a config file. I could never
work one out, and without a rule I can hold in my head, I look things up forever.

### The terminal UI

Nx 21 shipped a terminal UI and turned it on by default on supported terminals,
locally. It takes the screen, and
my scrollback goes with it. Selecting text stops behaving the way it does in
every other program in that window, so copying a stack trace out of build output
turns into a puzzle I have to solve first.

The Nx docs say the interface "is entirely controlled through keyboard
shortcuts," and tell me to press `?` for the list. To read the output of my own
build, I have to learn a keymap. It is the most asinine fucking developer
experience I have run into in years.

Search that page for the words scroll, select, or copy. I could not find them. I
think nobody weighed those questions, because whoever designed this looked at a
terminal and saw a rendering surface instead of an interface carrying fifty years
of contract. Frontend instincts pointed at a backend concern, rebuilding a
problem the terminal settled before I got here.

magus ships a text interface too, `magus diff --tui`. It renders inline, in your
scrollback, and it does not take over the screen or the scroll wheel: your
terminal keeps behaving like your terminal. That is the whole distinction. A
text interface that fights the terminal it lives in has already lost the
argument.

### Everything goes in the box

Nx wants everything inside its box, and while my work matches what the box
anticipated, I am fine. The moment I need something it did not anticipate, I
write an executor, and an executor is custom JavaScript. Plugins landed on top of
that later. So I end up writing TypeScript and carrying a build step for the thing
that runs my build steps. Nx abstracts parts of that away, and it is still a build
in front of my build.

Options make it worse. One option arriving at one task can come from the
executor's schema defaults, from `targetDefaults` in `nx.json`, from that
target's `options`, from whichever named `configuration` got picked, or from what
I typed. When the value reaching the process is not the one I expected, I go
digging through layers to find which won. Configuration inheritance has been
solved many times over, and the good versions all ship a command that prints the
resolved value next to where each piece of it came from.

There is a failure I hit over and over, and some of it is how Nx is wired into my
company's repo rather than Nx itself. Something gets rebased, the installed modules
drift out of sync, and the next command I run fails with an error that has nothing to
do with what is actually wrong. The fix is to reinstall. I have learned to reach for
that faster than I used to, but coming from Go it was never obvious that I was even
looking at a Node problem. My suspicion is that people who live in that ecosystem see
these often enough that they have stopped reading as broken.

### The layer I cannot build on

Nx core is MIT, and I want that on the record before I complain.

Remote caching is most of why anyone adopts a monorepo build tool, and it keeps
moving. Community task runners, then caching behind a paid Powerpack license in
v20, then free self-hosted plugins again in 20.8, then those deprecated in May
2026 over a cache-poisoning vulnerability (CVE-2025-36852) that Nx says is in
the packages' design and cannot be patched - and which, to be fair to Nx, they
note affects self-hosted cache plugins across many build systems, not only
theirs. Today the guidance
points at Nx Cloud, an Enterprise plan, or writing your own server against their
OpenAPI spec.

The vulnerability is real and I won't dress a security deprecation up as a
pricing decision. But land anywhere in that timeline and it stops mattering which
link was commercial and which was security. A foundational capability moved four
times in two years, and where it lands is a paid product.

I also think the shape that got deprecated was the wrong shape to begin with, and I
thought so before there was a CVE attached to it. Anyone who opens a pull request can
write to the shared cache, and everyone else then reads what they wrote. That is a
lovely quality-of-life win right up until you are trying to work out whether the thing
you are debugging is your machine, someone else's machine, or an artifact one of you
pulled down. When I run a build locally I want it to be local. I do not want a remote
cache silently answering for it.

CI is a different argument, because there you can gate who writes. The way magus does
it is the inverse of Nx: a run on main is what populates the shared cache, and a pull
request may only read from it. I should be straight about the trade, since I spent
this section complaining about someone else's: you get less out of it that way. The
quality-of-life win is smaller, and you only really feel the benefit at a scale where
you have a lot of pull requests touching genuinely different things. What you get back
is that nothing a stranger opened can write into the thing everybody trusts.

I don't understand how you write open source software on a build tool whose
foundational layer is gated. If I cannot hand my entire build to a stranger with
no account and no license, I have not handed them the project.

So: magus will never be monetized. No paid tier, no hosted plan, no open core, no
license key for the cache. That is settled rather than a stage I plan to grow out
of, and the reasoning is selfish as much as principled. A project with nothing to
sell has no reason to move a capability behind a wall later.

## Nix

[Nix](https://nixos.org) frustrated me more than Nx did, for a reason that has nothing to do with the
technology. The engineering is impressive and the people behind it are sharper
than I am.

I hit the CLI while `nix-env` was giving way to `nix profile`. Two vocabularies
live at once, answers online from both eras, no consistency to lean on. The
documentation never felt current either, which I put down to how fast they were
moving rather than to neglect, but it left me guessing.

I was also using it wrong, and I want that on the record before anyone else points
it out. I came at it imperatively when the entire point is to be declarative, so a
fair amount of that pain was self-inflicted. What I do not think was self-inflicted
is how inconsistent the interface felt: similar things invoked in dissimilar ways,
with no rule I could carry from one command to the next. Some of that is the cost of
a transition they were in the middle of. It was still miserable to learn, and it may
well be better now.

A CLI I can predict beats a CLI that is powerful in ways I have to look up every
time.

## Dagger

[Dagger](https://dagger.io) is the one I loved.

I want to be clear that this section is not a complaint. Building a container
without a Dockerfile, describing it in a real language instead, takes out a whole
category of foot guns people trip over every week. Layering stops being a thing you
hand-tune by reordering lines and hoping the cache holds. Small images stop being a
craft project. You write what the image is, idiomatically, and the tool works out
the rest. Their Go SDK is cleanly written, reads as idiomatic Go put together with
intent, and I still open it and look at it. They diagnosed the same problem I did
and shipped a lot of good ideas at it.

I saw past its rough edges because I thought what it was reaching for was worth the
trouble. What I could not do was get anyone else there. I showed it to my team and
they did not see it, and no amount of me being excited about small images moved
them. Dagger stays agnostic about your language, so it hands you an SDK in whichever
one you picked, and the seam where my code met theirs got wonky. That is a real
technical complaint, but it is not the one that mattered. I never got buy-in
internally and never saw it take hold elsewhere either, and that bothers me more
than any technical complaint here, because I still think they were right.

Part of why, I think, is how the problem got framed. I never read Dagger as a
replacement for Make or whatever orchestrator you already had. I read it as sitting
one layer deeper: the thing that builds the container, not the thing that decides
what runs when. That distinction never survived contact. People heard "CI agnostic"
and came away thinking they were being asked to throw out their build tool, and then
judged it on that basis. I had that same argument over and over and never fully
understood where the misconception was coming from, which is my honest answer rather
than a diagnosis of their marketing.

It has been about a year since I opened the Go SDK, so read all of that as a
snapshot, and I do not know how they position it now. The technical work was good.
Where I think it lost people is the story about which problem it was solving. And
magus owes Dagger a debt: I refined the formula hard and what came out is my own
take on it, but the influence is there.

## A build tool should not generate code

This is the position I am least willing to give up.

Nx puts `nx generate` right in the surface. Dagger works harder to hide it and it
is still there: authoring a module writes `dagger.gen.go` and an
`internal/dagger/` tree of typed client bindings next to your code, and the docs
recommend committing them, which the new module format requires.

Dagger also marks those files `linguist-generated` in `.gitattributes` so GitHub
folds them out of diffs, and I want to be clear that this is the correct thing to do.
magus does the same for its own generated output, and this repo carries those markers.
My objection is not the marking. It is who did the generating. In magus I am the one
who declared that output, in a target I wrote, and the tool does what I declared. With
Dagger the build tool put a file in my repository and told me to commit it.

None of this is new, which is the part that gets me. Autotools has worked this
way since long before I started: you write `configure.ac` and `Makefile.am`,
autoconf and automake turn those into a `configure` shell script and a
`Makefile.in`, and running `configure` turns that into the Makefile that make
finally reads. Generated code producing generated code producing the thing that
runs. CMake arrived later and does a tidier version of the same job,
turning `CMakeLists.txt` into Makefiles or Ninja files.

I want to be fair: it is impressive. A system that generates its own build
machinery and has it work across a hundred platforms is a real engineering
achievement and part of me enjoys looking at it. But when it breaks you need
every layer in your head at once, the macro language and the generated script and
the generated makefile and make itself, which is four things to understand before
you can fix one.

None of which is an argument against make. I still reach for it in 2026. Not for
everything and not for a monorepo, but it is on every machine I touch, it does what it
did twenty years ago, and it has never surprised me by moving underneath me. That
reliability is most of what I am asking for from anything newer.

I have to keep my own house in order here, because magus generates things.
`magus init` scaffolds a magusfile once and never touches it again. This repo
regenerates its own docs on a target I run when I want it. The line I hold: no
generated code sits between you and running a build. A magusfile is Buzz, and
magus interprets it. Nothing compiles it, nothing binds it, and no file lands in
your tree that you have to keep synchronized with a tool version.

Scaffolding hands you a file and leaves. A generator that runs as a condition of
the tool working never leaves.

A limit on my own knowledge, since I am drawing a line here: I do not know whether an
Nx generator scaffolds once and walks away the way `magus init` does, or whether it
keeps owning what it wrote. I never got far enough in to find out. Generating a custom
executor produced more files than I could hold in my head, and that was its own reason
to back off. So read the comparison as being about how much a tool hands you at once,
not as a claim about who owns it afterward.

Packaging is the same argument, and it has nothing to do with the language as a
language. It is about what your user sees when your CLI fails.

A JavaScript runtime stack trace is not an error message. It is the tool's
internals dumped on somebody who wanted to know what to do next, with the one
human-readable line buried in the middle of it, if it is there at all. We are in
2026 and this is still how a large share of command line tools report failure. I
do not understand why we are still writing CLIs in TypeScript.

Use the right tool for the job. A CLI is a program a stranger runs on a machine
you will never see, and that argues for something compiled, self-contained, and
able to fail in a sentence. magus ships as one statically linked binary. No
runtime, no `node_modules`, no second toolchain to install.

That last part stopped being a taste argument a while ago. A CLI that pulls a few
hundred transitive dependencies at install time is a CLI with a few hundred
chances to hand somebody else's code to your users. The npm ecosystem has been
learning that in public, repeatedly, through one supply chain compromise after
another, and the lesson finally looks like it is landing. It is a strange thing
to feel vindicated about, because everybody downstream paid for the lesson.

The frameworks that grew out of that ecosystem strike me the same way:
overengineered, and re-deriving things computer science settled decades ago.

We can do better than this as an industry, and I mean that as a challenge rather
than a complaint. Shipping a pile of code has never been easier, and it has never
been easier to mistake the pile for progress. Easy to write does not mean good,
well architected, or worth maintaining. Lines of code measures none of that. If
we are going to count anything, fewer lines is the number worth promoting.

That pressure got worse this year, not better. Writing a feature is close to free
now, so the only thing between a codebase and endless creep is somebody deciding not
to add one. Jeff Atwood said it in 2007 and it has only gotten truer:
[the best code is no code at all](https://blog.codinghorror.com/the-best-code-is-no-code-at-all/),
because every line you bring into the world has to be debugged, read, understood, and
supported. How cheap it was to write changes none of that.

There is research on why we are bad at this.
[People systematically overlook subtractive changes](https://www.nature.com/articles/s41586-021-03380-y),
a 2021 Nature paper, found that people handed a problem reach for what they can add
and barely consider what they could take away, even where removing something is the
better answer. It has a name, additive bias, and it turns up in every code review I
sit in. Deleting a feature reads as a loss. It is usually a gift to whoever
maintains the thing next, and we do not take it seriously enough.

Some of that pressure is financial. A company with investors has to show the line
going up every quarter, and "the tool is finished, it does what it should" does not
raise a round. So features keep landing after the useful ones are done, and
eventually you are shipping change at the people who liked it the way it was.
Continual growth is not sustainable. Continual feature development is that same
promise pointed at software, and it breaks the same way. There is a point where
enough is enough, and as code gets cheaper to write, having the forethought to
notice that point matters more, not less.

## Why explicit

There is a real market for the opposite approach, and I should be upfront that I am
not the person to make its case. I am not the intended user and never have been.
Tools that abstract the machinery away are, by every account I trust, a pleasure
inside the domain they were built for, and Nx is good there. Stay inside what the
authors imagined and you move fast. I follow that argument and I have never once
wanted it.

My honest read is that the trade is short sighted. It glosses over too much, and
when it breaks, it breaks badly.

Step outside it and the experience degrades all at once. The moment you need
something they did not anticipate, writing a file somewhere unexpected or tagging
something in a way their path does not cover, you are fighting the tool instead
of using it.

[Skaffold](https://skaffold.dev) is where I feel this most strongly, and I am not
going to pretend to be even-handed about it. It markets itself well and feels like
magic for about a week. Then you need something its authors did not picture, and
there is no seam to get at, because the machinery you need to reason about is the
machinery it exists to hide. To me it is the clearest example of everything this
section argues against. Plenty of people run it happily, so take that as my taste
rather than a defect report.

The failure has a shape. Engineers are drawn to abstracting things away, and I mean
that to include me: it is most of what we are trained to do, and it is usually the
right instinct. So one team wraps a tool, another team wraps that wrapper, and a
third builds on the second.
Each layer looks reasonable to whoever wrote it and none of them is obviously the
mistake, which is why the stack keeps growing until something small at the bottom
brings the whole thing down.

Abstracting well needs the whole picture, and layering is what takes the whole
picture away. Whoever writes layer three can see layer two. They cannot see the
tool at the bottom, its failure modes, or which of its features layer two quietly
dropped, so they end up abstracting over a model of the thing rather than the
thing itself.

[Temporal](https://temporal.io) says this out loud, which I respect. Their guidance is to avoid wrapping
their SDKs so deeply that you hide useful features or make them hard to upgrade.
A thin shim is fine. A friendly layer for newcomers is fine too, as long as those
people can still reach the SDK directly, and that last condition is what keeps it
honest.

So my position: a build tool should not hide the tools from you. You should be
able to see which command runs and how its arguments were assembled. Software
breaks, it breaks in ways nobody designed for, and at that moment the only thing
that matters is whether you can work out what happened. Every layer between you
and the actual command is a layer you have to peel back first, usually while
something is on fire.

Picture how this plays out. Someone writes an Nx executor built tightly around
one job, and it works. When it breaks, everyone has to go through that one
person, because they are the only one who can see what it does. That is a bus
factor of one, manufactured by the design. The person is not the problem; the
shape is.

At a large company with deep subject matter experts on every layer, this matters
less. For everyone else, seeing how the thing works is what lets you pick up the
pieces yourself instead of escalating to a maintainer or waiting on the one
person who understands it.

I think of it as the How It's Made problem. Be helpful, but not so helpful that
nobody using your tool ever learns anything from it. There is a line there, and I
would rather sit on the transparent side of it.

## Where the guardrails belong

Software is not perfect, and CI is the weakest link in most chains I have worked
in. There is a baseline of jank that most teams quietly sweep under the rug.

The response I keep seeing is to wall it off: describe everything in hermetic
JSON or YAML, permit nothing outside the schema, and expect it to hold. That is a
losing bet, because it asks you to be a fortune teller. You are anticipating what
your users need without fully understanding how they use the thing, and every
time you guess wrong the tool's answer is that they cannot do that here.

It also gives the developer no grace. There are days when you push something you
know is not right because the pipeline has to be green in the next ten minutes,
and that is part of the job. Pretending otherwise removes your tool from the
conversation without removing any of the pressure.

Underneath all of it is a posture. These are developer tools. The people running
them are capable adults who do this for a living, and a tool built as though they
cannot be trusted with a decision is going to lose them. Inform them instead.
Hand over everything you know: what will run, what it will touch, what you think
is wrong with the plan. Then let them choose. Whether their choice matches what I
consider best practice is not mine to settle, and sometimes the right answer for
somebody is the half fix they already know is a half fix, because the constraint
they are working under is a delivery date rather than an engineering one.

Security worked this out fifty years ago. [Saltzer and
Schroeder](https://web.mit.edu/Saltzer/www/publications/protection/) listed
psychological acceptability among their design principles in 1975: if the
protection mechanism makes the work harder than going without it, people route
around the mechanism, and the route they find is worse than whatever you were
guarding against. The password policy strict enough to get passwords written on
sticky notes is the classic example. Tighten past what the job can bear and you
do not get compliance, you get workarounds.

Build tooling behaves the same way, so magus puts its guardrails somewhere else.
The language you write targets in is a real language, with an escape hatch,
because sooner or later you need one. What contains it is the engine: a sandbox
that declares what a target may touch, and a content-addressed cache that makes a
run reproducible from its declared inputs. The constraint sits where it can be
enforced without anyone having to predict you.

It is also how you get out of the usual tradeoff. A tool should meet a new user
where they are without boxing in the person who has been doing this for fifteen
years, and I do not think those two are opposed. You get both by informing rather
than deciding: the newcomer follows what the tool told them, and the veteran
reads the same information and does something else with it.

## What I wanted instead

Bad developer experience gets under my skin and drives me mad. Input
latency in a command, a tool that makes me wait before I can tell it what I want,
an error that says something broke without saying what to do about it. I have had
enough of it, and that is most of why any of this exists.

So: a tool built for a human first. Behavior explicit rather than implicit.
Deterministic, so the same inputs give the same output and a second run of a
cached target changes nothing. Output I can read in a terminal and parse in a
pipe. Errors that tell me what to do next rather than what broke inside.

Agents can drive that, and I did not build any of it for them. An interface
legible to me is legible to them. I repeat work all day; an agent repeats it
faster.

## A good CLI does not need an MCP server

magus ships one. I built it, I use it, it works, and I am seriously considering
removing it.

The CLI already prints structured output: `-o json`, `-o name`,
`-o template=<go-template>`, and an output reference for anything that ran. Point an
agent at `magus ls -o json` and it gets what the MCP tool would have handed it. If
your CLI is consistent and predictable, a protocol layer on top has close to nothing
left to do. I think we got lazy over the last ten or fifteen years and started
treating a confusing CLI as the natural state of things, and an MCP server is often
filling a gap that should never have opened.

Two caveats, because I am painting broadly here. "Good CLI" is subjective, which is
why I named consistency and predictability rather than something fuzzier. And the
whole argument assumes your CLI can actually reach everything, which is not
automatic: magus has capabilities you get at through `magus buzz` rather than a
subcommand of their own, and keeping that surface honest is work I have to keep
doing. Where you are wrapping something you do not own or cannot change, a protocol
layer earns its place. But if your CLI needs an MCP server before an agent can drive
it, the problem is upstream of the MCP server.

## The PWA is an experiment

magus ships a web console. I built it as an experiment, to find out whether I get
genuine value out of having one. I am not convinced the answer is yes.

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

This is also where I have found the AI tooling genuinely useful, and I want to be
specific about it after spending most of this post complaining. The cost of building
something well enough to judge it used to be high enough that experiments like this
died as ideas. Now I can run the experiment and answer whether it becomes sticky with
a real thing in my hands instead of a guess.

## Why Buzz

A magusfile is written in Buzz, which is a strange enough pick that it deserves
an explanation.

I was not shopping for a language on ideology. I had a hard constraint: I have to
implement it. magusfiles run on my own Go implementation, so the language had to
be small enough for one person to write an interpreter for, and anything larger
would have been a much bigger feat. Getting a JIT working smoothly was hard
enough at that size. Small and statically typed was the requirement, and Buzz fit
it.

What decided it was the people. A small, dedicated group has been refining Buzz
and shipping releases for years without much of an audience, and I recognized the
work ethic in that. I would rather build on something maintained that way than on
something fashionable.

The choice started as a practical one. I did not expect the design of the
language itself to matter as much as it turned out to.

## Explicit beats familiar

Building magus handed me an accident I did not plan for.

There is close to nothing out there for a model to have trained on Buzz, so I
expected agents to be useless at it. Instead I get good code out of them with
little effort. Those same agents write JavaScript and TypeScript for me and I
spend the afternoon undoing it, and those two languages carry more training data
than anything else on earth.

I don't think that gap is about the models. Buzz gives you close to one way to
say a thing, which leaves little room to be clever and be wrong. TypeScript keeps
adding sugar, and I hate the sugar and what it teaches people to write.

I should own where that taste comes from. I love Go for the same reason, that
there is usually one way to write a thing, and plenty of people hate Go for
exactly that. This is a preference of mine, not a proof of anything.

Weigh that as my experience rather than a benchmark. It is still the strongest
evidence I have here: a language an agent has never seen beat the language it has
seen most, and explicitness is the only variable I can point at.

Someone will open this repo, find the TypeScript in the console, and tell me it
is bad TypeScript. Fair enough, and you are probably right. I use it at work, so
I need enough of it to stay useful, and this is where I get the practice.

## I built the knowledge graph for me

I did not build it so an agent could learn my monorepo. I built it so I can walk
into a repo I have never seen, ask what depends on what, ask why a target runs,
ask what a change would break, and learn the place myself. `magus query`,
`magus explain`, `magus path`, `magus insight`. Discovery tooling for someone who
is lost, which is me most of the time. A teammate on day one and an agent in a
fresh session have the same problem, and it is the problem I had.

A lot of projects will turn your codebase into a knowledge graph now, and most of
them build it with a separate indexer that scans the repo and infers structure. I
will not name any, because I would rather argue with the approach than send traffic
at a particular one.

The approach is what I think is wrong. An outside observer has to guess. It reads
your files, pattern-matches its way to what it thinks depends on what, and produces
something that looks authoritative and is actually a best effort. Nothing checks it.
There is no authority in the loop, because the thing that holds the authority is your
build tool, and the indexer is not that. So you get a second system that can be
wrong, and the drift between it and your build stays invisible until it bites
someone.

magus went the other way because it had no choice. A build tool already has to
know the repo precisely, down to every project, every target's declared inputs
and outputs, and what a diff reaches. Get that wrong and builds break, loudly.
That is what makes it the authority: it is not inferring the structure, it is the
thing that acts on it, and being wrong costs it immediately. The graph is that same
knowledge handed back as answers, checked every time anything runs, by the build
itself.

An agent reading the same graph is a byproduct I am happy about. The day I catch
myself designing that graph for an agent first is the day I have taken a wrong
turn.

## These are not new problems

I want to be honest about something, because it is the thing I find most tiring
about this whole moment. I do not get the names.

Same names as the ones at the top of this post. A new one arrives every few
months, and I do not follow what it is naming that the old term did not already
cover. Underneath each of them is a problem computer science has been
working on for fifty years. Partition work into units that do not conflict. Know
what a change affects so you can skip the rest. Cache on content instead of
timestamps. Give a process the smallest amount of state it needs to do its job.
Caching is hard. Concurrency is hard. The line usually attributed to Phil Karlton,
that there are [two hard things in computer science](https://martinfowler.com/bliki/TwoHardThings.html),
cache invalidation and naming things, has been a joke for decades precisely because
it keeps being true. These are old, hard problems with a lot of prior art behind
them, not new categories that showed up with agents. I am not trying to talk down to
anyone who reaches for one of these terms - I am saying, plainly, that I do not
understand why we need them.

Someone is going to point out that magus has its own vocabulary, and they are right
to. It has spells and charms and wards. The distinction I would defend is what those
words sit on top of. A spell is a thin wrapper over a tool you already run. It does
not replace `go test`, it does not hide it, and it does not ask you to learn a new
theory of what building is. You still have to understand your own toolchain, and
there is no shortcut in there where knowing what `go build` does stops being your
job. That is deliberate. The day I ship a concept you have to learn before you can
run your build is the day I have done the thing I am complaining about.

`magus affected ci --plan` splits the affected projects into shards that can run at
the same time. I wrote it to fan CI jobs across runners, and it feeds a GitHub
Actions job matrix. That is all it is. It turns out the same record answers the
question someone gets when they hand work to several agents at once: which units
can run together, and what does each one touch. I did not add anything for that. It
already knew, because a build tool that did not know would break builds.

There is one field in that output that is genuinely agent specific, which skill a
unit's work routes to, and it sits in its own `agents` key so that everything
around it reads as what it is. Ordinary build metadata. The invocation, the spells
it runs, the files it declares it writes. A person wants all of that too, and did
first.

The part I will concede, because it is real: agents made short, explicit,
machine-readable output matter more than it used to. A person skims past a wall of
text. An agent pays for it by the token, and a vague error costs a retry. That is a
genuine forcing function and it made me better at output design. But it sharpened
an old problem, it did not create a new field.

So when something here works well with an agent, the reason is boring. It is a
build tool that knows what it declared, says so in few words, and can hand you any
answer as text or JSON. That was the goal before, and it would still be the goal if
none of this had happened.

## What I take from suckless

A philosophy and a group of developers both go by suckless, and the core of it is
right: get software back to its roots and keep the bloat out. They write C, cap
how large a project may grow, and count deleted code as progress.

I am not there, and I want to say so before anyone else does. magus ships a
daemon, an MCP server, a knowledge graph, and a web console. I doubt Go is even a
language that can get there, given what it puts in a binary before I write my
first line.

So I take the discipline and leave the aesthetic. Be intentional about what you
add. Take on no more than you can maintain. Get the foundation right before you
put another floor on it.

That last part is where Nx lost me. The abstractions I disagreed with kept
compounding while the ground underneath kept moving, and at some point I stopped
reaching for it.

I should own my half of that. I never filed a bug and never took any of this to
their community. I assume other people raised some of it; I did not. So I cannot
tell you what they would have fixed if someone had asked, and a user who quietly
walks off is worth nothing to a maintainer. That is the honest shape of it. They
lost me, and I never gave them the chance to keep me.

Durability and consistency are what I want a reputation for. The tool that works
the way it worked last year, that you can put underneath something and stop
thinking about. You earn that by not moving, over years.

## A thing I am still working out

This sits under the whole post, so I want to say it out loud, and I would rather
open a conversation than land on a position.

Some context on where I am standing, because it colors all of this. I am a software
engineer on a platform engineering team, and the company I work for builds agentic
tooling. magus is my own project, built on my own time, and these opinions are mine
alone, but I am not writing about any of it from the outside.

I also got here late and reluctantly. I was skeptical of Copilot when it launched,
and it took me a long time to touch Claude at all.

I use agents every day and I have not settled how I feel about it. There is a
pressure now to keep using them, to justify the spend, to show the tokens went
somewhere, to avoid being the one seen as holding the team back. I feel that
pressure, and I don't hear many people saying so.

I am not sold on most of what gets promised about this technology, and plenty of
it has been oversold. Programming is probably the strongest case anyone has made
for any of it, which is part of why I keep reaching for the thing I am uneasy
about.

I read the code, I critique it, I review it, I tweak it. That is real work and I
won't pretend otherwise. It is not the same as it was. I miss writing code, I
don't write enough of it now, and I don't like that about how I work.

The worry underneath is atrophy. Skills behave like muscle, and there are some I
have stopped working. Plenty of what I hand off is remedial and I am glad to skip
it. But some of it was the hard part, and the hard part is where I learned. The
friction, the frustration, the afternoon lost to something stupid, and then the
click when it lands: that sequence is how I got whatever I know. I might be
trading a short jump ahead for being a worse engineer in ten years.

The other side of it is just as true. I have ADHD, and my head runs a lot of
threads at once on a good day. Ideas used to pile up faster than I could get to
them, and some just got lost before I found the time to write them down. These
tools close that gap some of the time. Some of what went out this year exists only
because of that. It unsettles me about as much as it
pleases me. Scary good, but scary.

For what it is worth: I used voice dictation and a model to get these thoughts
onto the page, then read every line and reworked it by hand. The same goes for the
project. I use a stupid amount of this tooling, and I leaned on it harder early on
than I do now. What actually got magus here was iteration, breaking a problem into
smaller and smaller pieces until the shape fell out, then doing it again. I review
every line at this point. If my name is on it, it is mine.

I am not writing any of this as an authority. I am working it out in public like
everyone else, and magus only reached a maturity I was comfortable sharing by my
grinding at it for a long time. Take the whole post as one person's findings so far.

Building these tools is how I get back to it. This is the work I still do with my
hands, and where I have come out for now is this: I build tools for humans that
happen to work for agents, because both are stuck on the same problems.

## An invitation

I am one person with opinions about build tools, and some of this is probably
off. If you work on Nx, Nix, or Dagger and you think I have it wrong, I would
rather hear it than not. Some of what I described may already be fixed, and some
of it I may have misread from outside. Tell me and I will correct the post.

I should say the obvious thing too, since writing a post like this invites it:
magus is not perfect and I am still refining it. It has rough edges, and there are
decisions in it I expect to revisit. I think it is a good step in the direction I
described, which is a different claim from thinking it has arrived, and I would
rather you hold me to the same standard I applied to everyone else here.

The one thing I would ask you to take from this: an AI integration bolted onto a
tool people already struggle with does not fix the tool. And the struggling is the
part that is easy to miss. Most people never file the bug, they just quietly stop
reaching for your thing, which is exactly what I did to Nx. If you are close enough
to a tool to maintain it, you are also the person least likely to hear that.

Abstract the amount of complexity your users can carry, print what you resolved and
where it came from, and the agents will come along for the ride.

Read the [changelog](../../changelog/), or start with the
[documentation](../../documentation/).
