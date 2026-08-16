---
title: "How I actually use AI tooling"
description: Not for the code, mostly. What it is good at, what I watch it for, and why accountability is the only rule that matters.
tags: [opinion]
date: 2026-08-04
draft: true
---

# How I actually use AI tooling

DRAFT. Seed material pulled out of the 0.4.0 post, where it was pulling the
argument off topic. Needs an opening, an ending, and a lot of work.

## Where this started: Goblin Tools

Possible opening for the piece, since it is the actual origin and it rhymes with
everything else I believe about tools.

Goblin Tools is a small site: a Swiss army knife of little single-purpose tools
aimed at neurodivergent people. One breaks a task into steps. One adjusts the
tone of something you wrote. Each does one thing. I started using them for daily
life rather than for work, and they are most of why I came around on any of this.

The theme is not subtle in hindsight. Small, sharp, purpose-built tools that do
one thing, which is the same thing I say about command line tools for the rest of
my life. It is the Unix philosophy pointed at executive function.

(VERIFY the specific tool names and the site's framing before publishing. Also
decide how much of the personal side to include; "neurodivergent" is the term I
prefer.)

## The thing that turned me

Worth putting early, because the piece needs the reader to know I came to this
slowly and against my own resistance.

I held out for a long time. Years. Skeptical of the whole thing, and slower still
to let any of it near code.

What broke it was a packaging problem, which figures, since dependency resolution
has been a lifelong fascination of mine. Some people are like that about trains.

The job: a set of Docker base images where base images depended on other base
images, across multiple architectures, fanned out into a dynamic GitHub Actions
job matrix and then fanned back in, joining the per-architecture builds into one
OCI manifest before anything reached the final registry. A DAG, plus a pile of
registry constraints I no longer remember precisely, at least some of which were
our own registry's limits at the time. (OCI artifacts and partial-image
publishing have both gotten much better since. This was harder then than it would
be now.)

I opened the ChatGPT web UI, gave it the constraints and the context, and asked
for a dependency graph. One file. Primitive and crude on purpose, because that is
what I asked for. I took it from there, abstracted it, cleaned up the packages.

It wrote Go. It compiled. First try.

That was the moment, and it is worth being exact about why. It did not solve my
problem. It got me to a working starting point on a problem I understood well
enough to judge the answer, which is the only reason I trusted any of it.

(Decide how much of the personal framing to keep. The fascination-with-package-
managers detail is good; how you label it is yours.)

## The frame: pair coding

This is probably the thesis of the whole piece, and the rest should hang off it.
Consider restructuring around this and folding "What I use it for" into it.

I have ADHD. I have a million racing ideas and I often cannot articulate them at
the speed I have them. This is the first time in my life I have been able to get
things out as fast as I think them. That alone would be worth something, but the
output is never the finished thing: I get it on paper, then iterate, refine, run
it back, over and over until it is presentable. The loop is the value, not the
generation.

The way I work now is pair coding, and I mean that literally. I use voice
dictation, which I had never used before in my life, and I talk. Back and forth.
I challenge it, I ask it to prove itself, I make it show me the source, I push
back the way I would with a coworker whose reasoning I respect but do not take on
faith. That mode is where it earns its keep. Handing it a prompt and accepting
what comes back is a different activity and I would not defend that one.

It is powerful enough to be a problem. I spend more hours at this machine than I
probably should.

## Vibe coding is where it started, not where it stayed

This is the answer to the atrophy worry, and it should probably sit right after
that section rather than float on its own. The Buzz work is the proof.

I knew I needed a JIT. That was where the speed was and I knew it was the
direction; I did not know how to get there. It started as vibe coding, and it did
not stay there.

What it turned into: I went and found white papers and writeups from people who
had built compilers and interpreters on their own time, including the honest ones
about what went wrong and which gotchas bite you in which language. I read them.
Then I fed them in, and we went back and forth over it.

It was slow. Deliberately slow, in very small steps, because I wanted the whys. I
wanted to know why a thing was done a particular way and not only that it worked,
and if I could not explain the reason I did not move on.

Then you solve enough tiny problems and one day the big problem is solved. That
is a beautiful thing, and it is the same feeling this work has always given me.
None of it felt like it was done for me.

## What it unlocked for other people

The genuinely good version of this: someone who is a deep subject matter expert
in their field but never had the technical chops to build software can now sit
down, talk to a model, and produce something that works. That is a real unlock
and I do not want to be sour about it.

If it never leaves their machine, I see no reason to worry about it at all.

At scale it is different, and an entire industry is going to grow out of cleaning
up after this: security review, remediation, the boring software fundamentals
nobody generated. If it has not exploded already it will.

## What I use it for

Most of what I use it for is not code. Getting a tone right, working out what I
think and then saying it plainly, breaking a pile of work into an order I can
start on. I get task paralysis, and something that helps me sort what matters
from what can wait has been worth more to me than any generated function.

Prioritizing pull requests: what is most important, what can wait.

Some research, though I check it.

## What I watch it for

The part I watch closely is the drafting. Left alone it will phrase me as an
authority on subjects I have no real background in, and I am not comfortable
standing behind that. Half of my editing is pulling claims back to what I can
defend.

## A little slop is fine

I have been called out for AI jank in my pull requests, and it left me
scrutinizing my own work to the point where shipping anything got hard. I think
we are overcorrecting as an industry. A little slop is fine, and always was,
which is what iteration and code review and the next commit are for. We could
give each other more grace about it.

## Accountability is the whole rule

If your name is on it, it is yours, whatever produced the first draft. A pull
request with your name on it is something you are answerable for, and that is the
part people are still working out.

So, plainly: did I use voice dictation and a model to get these thoughts onto the
page? Yes, completely, and I have no problem saying so. Did I read every line,
rework it by hand, cut what I could not defend, and end up with something I am
proud of and recognize as my own voice? Also yes. It is a tool. I would rather
treat it like one and stand behind the result than pretend I did this the hard
way.

## Follow-through

The reason I actually keep using this, and the one I have not seen anybody else
write about. Strong candidate for the emotional center of the piece.

My pattern, for as long as I can remember: get a project to ninety percent, feel
the hit, tell somebody about it, never touch it again. I have a graveyard of
things that were almost done. Telling people was the reward, so finishing stopped
being one.

So I work quietly now. Stealth mode, more or less, though magus is open source and
a couple of people have found it anyway, one of them an old coworker.

What changed is follow-through. Every project has a ridge where the interest runs
out and something shinier turns up, and clearing that ridge has been close to
insurmountable for me my whole life. This gives me the nudge over it. Not ideas,
which I have never been short of. The part where I keep going after the novelty
is gone.

That is not a use case anyone is selling, and it is the one that has mattered
most to me. magus is the furthest I have ever taken anything.

## Why I think it prevails, and it is narrow

I agree with the artists. Generated work is not art, art is inherently human, and
I am not going to argue that one.

The reason I think this technology stays is narrower and much less romantic: it
can write code. Knock down every other argument and that is the one left
standing. Code is math, and math is what these things have always been good at.
It was always math.

## The maintainer side, and why blanket bans miss

Some large, successful projects have taken a hard anti-AI stance on
contributions. I think that is extreme, and I also sympathize with how they got
there.

The human has always been the bottleneck. You can only review as fast as you have
reviewers, and that never scaled with how fast people could write code. What
changed is the ratio: contributions arrive faster than ever, and now a reviewer
also has to watch for the clever-looking pull request that is overengineered, or
that hides something malicious in code that reads fine. That is a harder job than
it was, and the people doing it are volunteers.

So I understand the ban. I still think there has to be a better answer than "no
AI," and I would rather spend effort finding it than defending the ban.

What I put in CONTRIBUTING.md instead: for your first couple of pull requests,
keep them small. Baby steps. Ask first. That is not an AI rule, it is a
trust-building rule, and it happens to solve most of the same problem.

Threads: what does a project actually need from a contributor to make review
tractable? Is disclosure useful, or does it just move the burden? Does "small
first PRs" generalize?

## Everything and nothing

Probably the closer. It ends on a question I cannot answer, which suits a piece
that has been about not being sure.

AI changed everything and it changed nothing, and the smallest example I have is
the em dash.

I use them now. My grammar got tighter, my sentences got a little more formal,
and the em dash turns out to be how I sometimes think. I have always leaned on
parentheses for the side thought, the thing running alongside the sentence,
because there is a lot going on in my head at once. The dash does a job the
parentheses were doing badly.

And then I delete them. Somebody will read one and decide a machine wrote this,
so I edit my own vernacular out of my own writing to avoid the accusation. That
is a strange thing to do to yourself, and I suspect I am not the only one doing
it.

They were always in the language. In the early days the dash was a tell, and it
is not a tell now, and the list of tells keeps moving. Wikipedia keeps a page on
signs of AI writing that reads like a moving target, because that is what it is.

At some point I do not think anyone will be able to tell, and the reason is
mundane: these things learned to write from us, and now their output goes back
into the pile they learn from. I am not qualified to say what that does to a
model and I am not going to pretend otherwise. Maybe it stays detectable forever.
I do not know. It is the part I find most interesting and the part I can say
least about.

(House-style question before publishing: magus bans em dashes in user-facing
strings and docs frontmatter, which is an ASCII rule for terminal output, not a
prose rule. Decide whether blog prose is covered. This section names the dash
rather than using one, which may be too cute for a piece about using them.)

## It is a yes-man

The failure mode I worry about most, and the reason pair coding has to mean
something rather than just sound good.

It agrees with you. Left alone it keeps agreeing with you, and an echo chamber of
one is a dangerous place to make decisions from. Everything above about
challenging it and making it prove itself is not intellectual posturing, it is
the countermeasure. If you are not pushing back you are not getting a second
opinion.

The safeguards are soft, too. What gets called a guardrail is a suggestion rather
than an enforcement: steering, not a boundary the program cannot cross. That is
fundamental to how these things work, not a gap somebody forgot to close.

Which is the same distinction I spend the rest of my life on, and worth saying
here because of that. A constraint that lives where it can be enforced is a
constraint. A constraint that lives where it can only be requested is a
preference with good intentions.

## We are all figuring this out

This should probably be the actual ending, with "Everything and nothing" moved up
ahead of it. The last line is the right note to go out on.

I do not want to be dramatic about it, but I cannot point to a precedent for the
last two years, and nobody has this worked out yet. Myself very much included.

The one place I do feel sure: I cannot fathom going through school with this, and
I would not have wanted to. Learning needs resistance. You need the friction, the
part where it does not work and you have to sit in it, and I think that matters
most when you are young and still forming. I have no background in biology and I
am not going to pretend I can tell you the mechanism. It is the same intuition
people have about handwriting versus typing, that the words come out the same and
something else does not.

So I think we may be doing real harm by putting this everywhere, and I assume it
is already deep in education at every level, which is where I suspect it belongs
least.

It is a tool. It is good in the hands of somebody who knows what they are doing
with it.

Which leaves the obvious problem, and it is the honest place to stop: you cannot
tell from the inside whether you are one of those people. Dunning and Kruger
wrote that one up. For all I know I have spent this entire essay generating slop.

## Threads to pick up

- The pressure to keep using it: justify the spend, show the tokens went
  somewhere, avoid being seen as holding the team back. (A short version of this
  stays in the 0.4.0 post; the long version belongs here.)
- Atrophy, and whether the friction that taught you is worth preserving on
  purpose. Same argument as "Why explicit" in the 0.4.0 post, aimed at yourself
  instead of at a tool.
- The counterweight: holding more threads at once than you ever could, and
  shipping them.
