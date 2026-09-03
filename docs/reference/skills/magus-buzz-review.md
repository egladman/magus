---
title: magus-buzz-review
generated_from: internal/agent/skills/magus-buzz-review/SKILL.md
description: "Review Buzz code - a magusfile, a spell, or a standalone .buzz script - across three lenses run in parallel: idiom/style, skeptic/correctness, and upstream-Buzz conformance."
tags: [agents, skills, magus-buzz-review]
skill_full_bytes: 20279
skill_simple_bytes: 15155
---

# magus-buzz-review

Review Buzz code - a magusfile, a spell, or a standalone .buzz script - across three lenses run in parallel: idiom/style, skeptic/correctness, and upstream-Buzz conformance. Use when asked to review, audit, or critique a .buzz file or change, or when a finding needs to say whether it holds anywhere Buzz runs (UPSTREAM), only under gopherbuzz (GOPHERBUZZ), or runs here but not upstream (PORTABILITY). Fans out the three lenses via the Agent tool and merges the results, the same shape go-review-ultra uses for Go. Does NOT cover magusfile/target/spell contracts - caching, ctx.needs, wards, charms; use magus-buzz-write for those.

Install it, rather than copying from this page:

```sh
magus agent install .claude/skills   # writes both forms below
```

An installed copy carries a provenance stamp, so `magus doctor` can tell you when a magus upgrade has made it stale. Text copied from this page carries none.

## What an installed copy carries

`magus agent install` writes this frontmatter above the body. `magus doctor` reads it to report whether your installed skills are current.

| field | value |
| --- | --- |
| `license` | `GPL-3.0-or-later` |
| `compatibility` | `any-agent` |
| `source` | `magus` |
| `agent-skill-version` | `51` |
| `knowledge-schema-version` | `10` |
| `skill-content` | `752d9e3b9c1e` |
| `skill-variant` | `full` |

The `skill-content` digest covers this skill alone, and both permutations below report it: they go stale together, never one silently, and a change to another skill does not move it.

## Full form

Every mechanical step spelled out, plus the rationale for each. Installed as the `<name>-full` twin: loaded by name rather than always, so a reader who needs the long form can ask for it without every session carrying it.

````markdown
# Reviewing Buzz code

magus-buzz-write teaches how to WRITE Buzz. This is for REVIEWING it - a magusfile, a
spell, or a standalone `.buzz` script - across three lenses run in parallel,
the same fan-out-and-merge shape go-review-ultra uses for Go.

Do not use this for magusfile/target/spell CONTRACTS: caching, `ctx.needs`,
wards, op kinds, charms, what makes something a command vs a service. That is
magus-buzz-write's territory and it already covers it; restating it here would only
drift out of sync with it. This skill covers the LANGUAGE underneath those
contracts: is the code idiomatic, is it correct, does it run where the author
thinks it runs.

## Authority labels

Every finding in every lens carries one of three labels, because "this is
wrong" means a different thing in Buzz depending on which authority backs
it - and a reader cannot act on an unlabeled finding, only argue about it:

- **UPSTREAM** - true of Buzz itself, wherever it runs.
- **GOPHERBUZZ** - true only of gopherbuzz, the implementation magus embeds;
  upstream Buzz may not have the construct, or may resolve it differently.
- **PORTABILITY** - runs fine here, under gopherbuzz, but will not parse or
  behave the same against upstream Buzz.

Label every finding from Lens 2 and Lens 3. Do not invent a fourth category,
and do not leave one unlabeled because the answer felt obvious.

## Establish the surface before applying anything

This is the single most important step in the whole skill: getting it wrong
produces confident, fluent false positives, because the "violation" really did
compile and run. Buzz parses in one of two modes, and most of what Lens 2 and
Lens 3 check for only applies to one of them:

- **Strict** (upstream parity) - rejects top-level control flow
  (`if`/`while`/`for`/`foreach`/`do-until`/`try`/`throw`/`return`/`break`/`continue`/
  a bare block) outside a function, and requires every call argument after the
  first to be labeled (`name: value`) unless it is a bare identifier. This is
  `magus buzz <file>`'s default.
- **Embedded** (relaxed) - neither restriction applies. `magus buzz --embedded`
  opts a script in; a magusfile and a spell are embedded UNCONDITIONALLY, with
  no opt-out - every code path that loads one passes the embedded option before
  the author's own code runs, so there is no author-visible switch to get wrong.

So: **a magusfile or a spell file is always embedded.** A top-level `if`, a
top-level `foreach`, an unlabeled second argument - all fine, all idiomatic,
in that file. Applying strict-mode rules to a magusfile is not a strict
reading, it is a wrong one.

A standalone script has to be judged by how it is actually invoked, not by
guessing from its shape:

- Runs via a bare `magus buzz <file>` (no `--embedded`) - strict rules apply
  for real; a top-level `if` there is a genuine defect.
- Runs via `magus buzz --embedded <file>`, is invoked from inside another Buzz
  program (`magus\cmd("buzz", ...)`), or its own header comment says which
  surface it targets - strict rules do not apply.
- Unclear how it runs - check the CI workflow or wrapper that calls it before
  flagging a strict-mode violation. A script that happens to have no
  top-level control flow and no unlabeled second argument is ALSO valid
  embedded Buzz, so its contents alone never prove which mode the author
  wrote it for - only the invocation does.

A conformance-suite fixture - a file whose whole purpose is exercising one
upstream-vs-gopherbuzz difference (generics, `match`, a bare `as` cast,
`::<T>`, ...) - is not a defect for using the construct it exists to test.
That is the fixture doing its job.

## Lens: idiom and style

What reads as Buzz house style versus what merely parses.

- **Namespace access on an imported module should be a backslash, not a
  dot.** `fs\readFile(...)`, not `fs.readFile(...)`. Authority: PORTABILITY
  (see Lens 3 for the parse-level reason) and also a plain readability call -
  `path\join(a, b)` and `record.join(a, b)` parse to visually identical
  postfix chains, and only the token distinguishes "call into an imported
  module" from "call a method on this value". A stray dot on an import target
  reads as a value method call to anyone who has not memorized which name is
  a module.
  FALSE-POSITIVE GUARD: a local variable, field, or ctx member that happens
  to share a module's bare name - `path`, `env`, `os` are common ones - uses a
  dot correctly. `path.endsWith(".buzz")` where `path` is a local `str` is
  ordinary member access, not a namespace-access mistake, even though `path`
  is also an importable module name. Only flag the dot when the receiver is
  the actual imported module identifier itself.
- **A flat import (`import "x" as _`) is a finding.** The `_` alias binds no
  name and merges every export of `x` into the importing scope, so a call site
  reads `escapeAttr(s)` with nothing on the line, or anywhere in the file, saying
  where `escapeAttr` came from. The reader's only recourse is to grep each
  candidate module in turn, and that cost is paid per call site rather than per
  import. It also makes the module boundary unenforceable in the direction that
  matters: adding an export to a flat-imported module can silently shadow or
  collide with a name in every importer, and nothing at the import line records
  which names were claimed.
  Prefer a named import and namespaced calls (`import "lib/text" as text;` then
  `text\escapeAttr(s)`). Where a specific unprefixed name is genuinely wanted,
  the selective form `import escapeAttr, slugify from "lib/text";` states exactly
  what enters the scope, which is the part `as _` throws away.
  Note the flat and selective forms are BOTH excluded from
  unused-import tracking (BZZ3001) - a flat import has no bound name to mark
  unused - so an `as _` that has stopped being needed will never be reported.
  Weigh the finding by blast radius, not by count: a leaf module flat-importing
  one helper is a nit, while a tree where every file flat-imports every other is
  a single structural finding, not one per line.
- **`export fun test(...)` is not a naming smell.** `test` is a contextual
  soft keyword rather than a reserved one, specifically so magus's canonical
  test-target name stays usable. Authority: GOPHERBUZZ - it is the one
  deliberate place gopherbuzz is a superset of upstream's reserved-word list.
  Do not flag it, and do not suggest renaming it.
- **A name that fails to parse with a name-shaped error might be a reserved
  word, not a deeper bug.** gopherbuzz rejects binding a variable, parameter,
  function, or enum case to: `out`, `from`, `match`, `pat`, `fib`, `rg`,
  `obj`, `ud`, `zdef`, `typeof`, `type`, `protocol`, `static`, `extern`,
  `double`, `any`, `Function`, `int`, `str`, `bool`, `void`. Authority:
  GOPHERBUZZ - this is gopherbuzz's own enforced list, kept for strict parity
  with upstream's reservations; it is not guaranteed identical to whatever
  upstream itself reserves. They remain fine in non-binding position (a map
  key, a member name, a type name) - only binding a NAME to one of them fails.
- **Object literal vs map literal is a common transcription slip from
  JSON-familiar authors.** `Point{ x = 1 }` (object literal, `=`) and
  `{"key": value}` (map literal, `:`) look alike and are not interchangeable.
  Flag a literal that mixes the two forms, or uses `:` where an object
  literal was clearly intended. Authority: UPSTREAM (both forms and the
  distinction are upstream Buzz).
- **A magusfile carrying logic that wants a test is a finding.** A
  magusfile is declarative configuration; a test of it tests your
  configuration, not your logic. The fix is moving that logic into a
  spell or a sibling module - see magus-buzz-write's "Test what you write".

## Lens: skeptic and correctness

Bugs and silent failures, not style. Read every function assuming it has one.

- **A `magus\Context.needs`-adjacent branch on a diagnostic code that names no
  real code.** The documented idiom for handling a magus failure in Buzz is
  to catch, then branch on `e["code"]` rather than matching `e["message"]` -
  a transposed code (`"MSG3003"` for `"MGS3003"`) or a stale one silently
  never matches, and the `catch` block still reads as live error handling
  while being dead code. A code cited only in a comment rots the same way,
  slower. Authority: GOPHERBUZZ/MAGUS, not upstream - `MGSxxxx` and `BZZxxxx`
  are magus's and gopherbuzz's own diagnostic codes, not a Buzz language
  concept.
  CHECK: any string matching `MGS[0-9]{4}` or `BZZ[0-9]{4}` in a `.buzz`
  file, in code or in a comment, against the documented set: `docs/reference/codes/`
  (organized by family - auth, charms, knowledge, magusfile, outputref, race,
  sandbox, services - one page per `MGSxxxx` code) for magus's own codes, and
  `libs/gopherbuzz/docs/codes/` (flat, one page per `BZZxxxx` code) for
  gopherbuzz's. A code that names no file in either tree is the finding.
  A code cited as DATA - a fixture or table row that names a real code as an
  example, rather than comparing against a caught error - is not a defect
  just because it is not itself a comparison; check whether the code exists,
  not whether the surrounding line is a comparison.
  Do not turn this into "type diagnostic codes as an enum": `Arg.Enum` scopes
  itself to a parameter whose values are a closed set, this set is not closed
  (it grows every release across two independently-versioned namespaces), the
  real read site is a map key off a caught error rather than a function
  parameter, and an enum case an older magus release lacks fails to LOAD -
  worse than a string that silently never matches. A closed set like a sign
  algorithm name is what `Arg.Enum` is for; a diagnostic code is not that
  shape.
- **A force-unwrap (`!`) on a value the type says can be null.** Buzz's
  version of a nil deref: `maybeUser!.name` panics at runtime the moment
  `maybeUser` really is null, same as an unchecked pointer deref. Prefer
  `?.`/`??` unless the caller has just proven non-null a line above. Authority:
  UPSTREAM (the operators and the risk are the same wherever Buzz runs).
- **A `catch` that discards `e` without inspecting `code` or `message`,
  where the failure can mean more than one thing.** Swallowing every error
  the same way is how a gate stops being a gate - the same failure mode
  go-review-skeptic flags for a Go `default` case that silently no-ops.
  Authority: UPSTREAM.
- **A compound assignment (`x op= v`) whose target has a side effect**, e.g.
  `f().count += 1`. Authority: GOPHERBUZZ - upstream evaluates the target
  ONCE; gopherbuzz evaluates it TWICE, so `f()` runs twice and any side
  effect in it (a mutation, a log line, a counter) happens twice too. This is
  a real bug under gopherbuzz specifically, not just a portability note - flag
  it as a defect whenever the target expression is not a bare variable.
- **A `match` treated as exhaustive, or an `obj{...}`/protocol annotation
  treated as enforced.** gopherbuzz's checker does not enforce match
  exhaustiveness, protocol conformance, or `obj{...}` shape annotations - they
  are accepted syntax, not verified ones. Authority: GOPHERBUZZ. Manually
  check that a `match` covers every case the matched type admits, the way
  go-review-skeptic manually checks a Go type switch for a missing `default`;
  a clean compile proves nothing here.
- **Trusting "it compiled" as proof the code is valid upstream Buzz, or
  "it failed to compile" as proof it is not.** Authority: GOPHERBUZZ.
  gopherbuzz implements a SUBSET of upstream Buzz, so passing today does not
  mean upstream would accept it, and a chunk of upstream's own compile-error
  fixtures compile clean under gopherbuzz anyway - a clean gopherbuzz compile
  is evidence, not verification, in either direction.

## Lens: upstream conformance

Named divergences between gopherbuzz and upstream Buzz. Each is a real,
observed behavior difference, not a hypothetical - use them to judge whether
code an author believes is "just Buzz" will actually survive contact with
upstream, and to stop a gopherbuzz-only behavior from being taught as if it
were the language.

- **Namespace access: `\` is the only form upstream recognizes for an
  imported module; `.` is not.** Authority: PORTABILITY. Upstream's own
  parser gives backslash a dedicated production for resolving a name against
  an import and gives the bare dot no such role at all - it is the general
  postfix member operator on VALUES. gopherbuzz's parser accepts both forms
  identically for a module reference, which is a superset, not a mirror:
  `fs\readFile(...)` parses in both; `fs.readFile(...)` parses only here.
- **A string is indexed by BYTES, and `utf8Len()` is the rune count.**
  Authority: UPSTREAM. `len()`, `sub()`, `indexOf()`, `byte()` and `foreach`
  all work in bytes, matching upstream's own builtins; `utf8Len()` is the only
  codepoint-counting member. So `"h\u00e9llo".len()` is 6, not 5.
  This is worth knowing because gopherbuzz USED to index runes, and
  code written against that reads plausibly either way. A loop slicing with
  `sub()` and bounding with `len()` was consistent under both models, so it will
  not announce the change; what moves is any index arithmetic that assumed one
  character was one position. Reach for `utf8Len()` only when the question is
  genuinely "how many characters", which is rarer than it looks - a byte count
  is what a digest, an encoding, or a wire format wants.
- **A bare `as` cast coerces in gopherbuzz; upstream statically checks it.**
  Authority: GOPHERBUZZ. `3.9 as int` silently truncates to `3` here; upstream
  rejects a cast that cannot hold statically. `as?` is the real type test in
  both and does not have this gap - prefer it when the intent is "test", not
  "coerce and hope".
- **Compound assignment double-evaluates its target in gopherbuzz; upstream
  evaluates it once.** Authority: GOPHERBUZZ. See Lens 2 - flagged there as a
  correctness bug when the target has a side effect, flagged here as the
  reason it will not misbehave the same way upstream.
- **A declared `!>` error set enforces PRESENCE but not TYPE.**
  Authority: GOPHERBUZZ. Upstream Buzz treats `!> ErrType` as a real error set;
  gopherbuzz checks only that a raising call is propagated or caught, never what
  it raises. Calling a `!> str` function from one declaring no raise at all is
  BZZ1006, "call may raise but is neither declared with !> nor caught" - a real
  gate, and a common one: it is what a script invoked by `magus buzz`
  trips on when it calls something like `fs\listDir` without declaring
  `!>`. But a function declaring `!> int` may throw a `str` through it and
  nothing objects, so the named type is documentation while the arrow
  itself is checked.
  So: do not treat a missing `!>` as proof a call cannot raise, and do not trust
  the NAMED type. Both were measured against gopherbuzz; this bullet
  previously said the whole annotation was "parsed and thrown away, nothing
  enforces it", which sent reviewers straight past every BZZ1006-class defect.
- **An anonymous object field shadows a same-named builtin method.**
  Authority: GOPHERBUZZ. `rec.map` reads the FIELD `map` if the object was
  built with one, not the builtin `.map()` transform - the field wins. A
  field named after a common builtin method name (`map`, `len`, ...) is worth
  a second look for exactly this reason.
- **A malformed `{...}` inside a backtick-interpolated string is a parse
  error upstream; gopherbuzz leaves it as literal text.** Authority:
  GOPHERBUZZ. A typo inside an interpolation placeholder fails loudly
  upstream and fails silently (renders the literal braces) here - review a
  backtick string's interpolated parts as carefully as you would review
  regular code, since gopherbuzz will not catch a malformed one for you.
- **Generics are erased at runtime in gopherbuzz; upstream reifies them.**
  Authority: GOPHERBUZZ. A `::<T>` type argument is parsed and then ignored -
  it exists for the static checker's benefit, not the VM's. Code that
  branches on a generic type argument at runtime cannot work here regardless
  of what upstream would do with it.
- **`pat.replace` replaces only the first match; `pat.replaceAll` replaces
  every match.** Authority: UPSTREAM - gopherbuzz mirrors this faithfully.
  Do not flag it as a bug and do not propose "fixing" `.replace` to replace
  everything; that would be the divergence, not the correction.
- **`str.replace` replaces every occurrence, matching upstream.** Authority:
  UPSTREAM. An earlier gopherbuzz version replaced only the first occurrence;
  that was a bug, and it is fixed. Do not teach or flag the old first-only
  behavior as current.
- **`test "..." {}` is genuine upstream syntax**, present in upstream's own
  test corpus. Authority: UPSTREAM. It is not a gopherbuzz invention - contrast
  with `test` staying bindable as a name (Lens 1), which IS gopherbuzz-only.
- **`assert`, `suite`, `testing`, and `assertcore` have no upstream
  counterpart.** Authority: PORTABILITY. They are gopherbuzz's own test
  surface, not a reimplementation of an upstream module - code that leans on
  their exact API or semantics has no upstream equivalent to fall back to,
  by design, not by omission.

### If you are reviewing gopherbuzz's own implementation

The above applies to reviewing a workspace's magusfile or spells; skip this
if that is all you are doing. If the change under review touches gopherbuzz
itself, do not quote a conformance score from a code comment - a comment has
carried a wrong number before, and a second stale number lived in the test
file at the same time. The one authoritative count is the line count of the
checked-in upstream-behavior allowlist the conformance test enforces in both
directions: it fails if a passing file regresses, and it also fails if a
newly-passing file is not added to the list, so the list can only be as stale
as the last test run.

## Running the three lenses

Spawn three `Agent` tool calls **in a single message** (parallel),
`subagent_type: general-purpose`. A subagent cannot invoke the Skill tool, so
it needs the section text, not the skill's name.

Prompt template per subagent:

```text
Read the "Lens: <idiom and style|skeptic and correctness|upstream conformance>"
section of the installed magus-buzz-review skill (.claude/skills/magus-buzz-review/SKILL.md,
or wherever this workspace installed it) and apply it to <target file/dir>.
Establish the surface first (magusfile/spell = always embedded; a standalone
script = check how it is invoked) before applying any strict-mode-derived rule.
Return findings only - file:line, the authority label, what's wrong, severity.
No code. Do not re-explore beyond <target>.
```

If the target is a whole workspace rather than one file, name the entry
points to walk (`magusfile.buzz`, `spells/**/spell.buzz`) rather than leaving
scope open-ended; an unscoped subagent re-derives the workspace layout three
times instead of once.

## Merging the findings

- **Group by lens.** Keep the three sections separate - they are different
  angles and collapsing them loses which authority backed which finding.
- **Dedupe by `file:line` within a section only.** The same line flagged by
  two lenses for different reasons (idiom's readability angle and
  conformance's portability angle on the same namespace-access mistake, say)
  stays as two findings - that is two lenses agreeing, not one lens repeating
  itself.
- **Combined severity table** at the end, drawn from all three sections.
  A Lens 2 finding with a real side effect (the compound-assignment
  double-evaluation, a swallowed error that matters) outranks a Lens 1
  readability note.
- **Top 1-3 to action first**, opinionated, across all three lenses.

## What this skill does not do

- Magusfile/target/spell contracts - caching, `ctx.needs`, wards, charms, what
  makes an op a service. Use magus-buzz-write.
- Write code or apply fixes. Output is a merged findings report.
- Teach Buzz syntax from scratch. Use magus-buzz-write for that, and point a reader
  there when a finding needs the "how do I write it correctly" answer rather
  than "here is what's wrong".
````

## Short form

The enumeration dropped, the judgment kept - for the most capable readers, not the least; the bar under the heading above shows by how much. This is the always-loaded primary. Both are hand-authored from one source body; see [Skills](../../guides/integrations/agents/skills.md) for the difference.

<details>
<summary>Show the short form</summary>

````markdown
# Reviewing Buzz code

magus-buzz-write teaches how to WRITE Buzz. This is for REVIEWING it - a magusfile, a
spell, or a standalone `.buzz` script - across three lenses run in parallel.

Do not use this for magusfile/target/spell CONTRACTS: caching, `ctx.needs`,
wards, op kinds, charms, what makes something a command vs a service. That is
magus-buzz-write's territory. This skill covers the LANGUAGE underneath those
contracts: is the code idiomatic, is it correct, does it run where the author
thinks it runs.

## Authority labels

Every finding in every lens carries one of three labels, because "this is
wrong" means a different thing in Buzz depending on which authority backs
it:

- **UPSTREAM** - true of Buzz itself, wherever it runs.
- **GOPHERBUZZ** - true only of gopherbuzz, the implementation magus embeds;
  upstream Buzz may not have the construct, or may resolve it differently.
- **PORTABILITY** - runs fine here, under gopherbuzz, but will not parse or
  behave the same against upstream Buzz.

Label every finding from Lens 2 and Lens 3. Do not invent a fourth category,
and do not leave one unlabeled because the answer felt obvious.

## Establish the surface before applying anything

 Buzz parses in one of two modes, and most of what Lens 2 and
Lens 3 check for only applies to one of them:

- **Strict** (upstream parity) - rejects top-level control flow
  (`if`/`while`/`for`/`foreach`/`do-until`/`try`/`throw`/`return`/`break`/`continue`/
  a bare block) outside a function, and requires every call argument after the
  first to be labeled (`name: value`) unless it is a bare identifier. This is
  `magus buzz <file>`'s default.
- **Embedded** (relaxed) - neither restriction applies. `magus buzz --embedded`
  opts a script in; a magusfile and a spell are embedded UNCONDITIONALLY, with
  no opt-out.

So: **a magusfile or a spell file is always embedded.** A top-level `if`, a
top-level `foreach`, an unlabeled second argument - all fine, all idiomatic,
in that file. Applying strict-mode rules to a magusfile is not a strict
reading, it is a wrong one.

A standalone script has to be judged by how it is actually invoked:

- Runs via a bare `magus buzz <file>` (no `--embedded`) - strict rules apply
  for real; a top-level `if` there is a genuine defect.
- Runs via `magus buzz --embedded <file>`, is invoked from inside another Buzz
  program (`magus\cmd("buzz", ...)`), or its own header comment says which
  surface it targets - strict rules do not apply.
- Unclear how it runs - check the CI workflow or wrapper that calls it before
  flagging a strict-mode violation.

A conformance-suite fixture - a file whose whole purpose is exercising one
upstream-vs-gopherbuzz difference (generics, `match`, a bare `as` cast,
`::<T>`, ...) - is not a defect for using the construct it exists to test.
That is the fixture doing its job.

## Lens: idiom and style

- **Namespace access on an imported module should be a backslash, not a
  dot.** `fs\readFile(...)`, not `fs.readFile(...)`. Authority: PORTABILITY
  (see Lens 3 for the parse-level reason) and also a plain readability call -
  `path\join(a, b)` and `record.join(a, b)` parse to visually identical
  postfix chains, and only the token distinguishes "call into an imported
  module" from "call a method on this value". A stray dot on an import target
  reads as a value method call to anyone who has not memorized which name is
  a module.

- **A flat import (`import "x" as _`) is a finding.** The `_` alias binds no
  name and merges every export of `x` into the importing scope, so a call site
  reads `escapeAttr(s)` with nothing on the line, or anywhere in the file, saying
  where `escapeAttr` came from. The reader's only recourse is to grep each
  candidate module in turn, and that cost is paid per call site rather than per
  import. It also makes the module boundary unenforceable in the direction that
  matters: adding an export to a flat-imported module can silently shadow or
  collide with a name in every importer, and nothing at the import line records
  which names were claimed.
  Prefer a named import and namespaced calls (`import "lib/text" as text;` then
  `text\escapeAttr(s)`). Where a specific unprefixed name is genuinely wanted,
  the selective form `import escapeAttr, slugify from "lib/text";` states exactly
  what enters the scope, which is the part `as _` throws away.

- **`export fun test(...)` is not a naming smell.** `test` is a contextual
  soft keyword rather than a reserved one, specifically so magus's canonical
  test-target name stays usable. Authority: GOPHERBUZZ - it is the one
  deliberate place gopherbuzz is a superset of upstream's reserved-word list.
  Do not flag it, and do not suggest renaming it.
- **A name that fails to parse with a name-shaped error might be a reserved
  word, not a deeper bug.** gopherbuzz rejects binding a variable, parameter,
  function, or enum case to: `out`, `from`, `match`, `pat`, `fib`, `rg`,
  `obj`, `ud`, `zdef`, `typeof`, `type`, `protocol`, `static`, `extern`,
  `double`, `any`, `Function`, `int`, `str`, `bool`, `void`. Authority:
  GOPHERBUZZ - this is gopherbuzz's own enforced list, kept for strict parity
  with upstream's reservations; it is not guaranteed identical to whatever
  upstream itself reserves. They remain fine in non-binding position (a map
  key, a member name, a type name) - only binding a NAME to one of them fails.
- **Object literal vs map literal is a common transcription slip from
  JSON-familiar authors.** `Point{ x = 1 }` (object literal, `=`) and
  `{"key": value}` (map literal, `:`) look alike and are not interchangeable.
  Flag a literal that mixes the two forms, or uses `:` where an object
  literal was clearly intended. Authority: UPSTREAM (both forms and the
  distinction are upstream Buzz).
- **A magusfile carrying logic that wants a test is a finding.** The fix is moving that logic into a
  spell or a sibling module - see magus-buzz-write's "Test what you write".

## Lens: skeptic and correctness

- **A `magus\Context.needs`-adjacent branch on a diagnostic code that names no
  real code.** The documented idiom for handling a magus failure in Buzz is
  to catch, then branch on `e["code"]` rather than matching `e["message"]` -
  a transposed code (`"MSG3003"` for `"MGS3003"`) or a stale one silently
  never matches, and the `catch` block still reads as live error handling
  while being dead code. A code cited only in a comment rots the same way,
  slower. Authority: GOPHERBUZZ/MAGUS, not upstream - `MGSxxxx` and `BZZxxxx`
  are magus's and gopherbuzz's own diagnostic codes, not a Buzz language
  concept.

- **A force-unwrap (`!`) on a value the type says can be null.** Buzz's
  version of a nil deref: `maybeUser!.name` panics at runtime the moment
  `maybeUser` really is null, same as an unchecked pointer deref. Prefer
  `?.`/`??` unless the caller has just proven non-null a line above. Authority:
  UPSTREAM (the operators and the risk are the same wherever Buzz runs).
- **A `catch` that discards `e` without inspecting `code` or `message`,
  where the failure can mean more than one thing.** Swallowing every error
  the same way is how a gate stops being a gate - the same failure mode
  go-review-skeptic flags for a Go `default` case that silently no-ops.
  Authority: UPSTREAM.
- **A compound assignment (`x op= v`) whose target has a side effect**, e.g.
  `f().count += 1`. Authority: GOPHERBUZZ - upstream evaluates the target
  ONCE; gopherbuzz evaluates it TWICE, so `f()` runs twice and any side
  effect in it (a mutation, a log line, a counter) happens twice too. This is
  a real bug under gopherbuzz specifically, not just a portability note - flag
  it as a defect whenever the target expression is not a bare variable.
- **A `match` treated as exhaustive, or an `obj{...}`/protocol annotation
  treated as enforced.** gopherbuzz's checker does not enforce match
  exhaustiveness, protocol conformance, or `obj{...}` shape annotations - they
  are accepted syntax, not verified ones. Authority: GOPHERBUZZ. Manually
  check that a `match` covers every case the matched type admits, the way
  go-review-skeptic manually checks a Go type switch for a missing `default`;
  a clean compile proves nothing here.
- **Trusting "it compiled" as proof the code is valid upstream Buzz, or
  "it failed to compile" as proof it is not.** Authority: GOPHERBUZZ.
  gopherbuzz implements a SUBSET of upstream Buzz, so passing today does not
  mean upstream would accept it, and a chunk of upstream's own compile-error
  fixtures compile clean under gopherbuzz anyway - a clean gopherbuzz compile
  is evidence, not verification, in either direction.

## Lens: upstream conformance

- **Namespace access: `\` is the only form upstream recognizes for an
  imported module; `.` is not.** Authority: PORTABILITY. Upstream's own
  parser gives backslash a dedicated production for resolving a name against
  an import and gives the bare dot no such role at all - it is the general
  postfix member operator on VALUES. gopherbuzz's parser accepts both forms
  identically for a module reference, which is a superset, not a mirror:
  `fs\readFile(...)` parses in both; `fs.readFile(...)` parses only here.
- **A string is indexed by BYTES, and `utf8Len()` is the rune count.**
  Authority: UPSTREAM. `len()`, `sub()`, `indexOf()`, `byte()` and `foreach`
  all work in bytes, matching upstream's own builtins; `utf8Len()` is the only
  codepoint-counting member. So `"h\u00e9llo".len()` is 6, not 5.

- **A bare `as` cast coerces in gopherbuzz; upstream statically checks it.**
  Authority: GOPHERBUZZ. `3.9 as int` silently truncates to `3` here; upstream
  rejects a cast that cannot hold statically. `as?` is the real type test in
  both and does not have this gap - prefer it when the intent is "test", not
  "coerce and hope".
- **Compound assignment double-evaluates its target in gopherbuzz; upstream
  evaluates it once.** Authority: GOPHERBUZZ. See Lens 2 - flagged there as a
  correctness bug when the target has a side effect, flagged here as the
  reason it will not misbehave the same way upstream.
- **A declared `!>` error set enforces PRESENCE but not TYPE.**
  Authority: GOPHERBUZZ. Upstream Buzz treats `!> ErrType` as a real error set;
  gopherbuzz checks only that a raising call is propagated or caught, never what
  it raises. Calling a `!> str` function from one declaring no raise at all is
  BZZ1006, "call may raise but is neither declared with !> nor caught" - a real
  gate, and a common one. But a function declaring `!> int` may throw a `str` through it and
  nothing objects.
  So: do not treat a missing `!>` as proof a call cannot raise, and do not trust
  the NAMED type. Both were measured against gopherbuzz.
- **An anonymous object field shadows a same-named builtin method.**
  Authority: GOPHERBUZZ. `rec.map` reads the FIELD `map` if the object was
  built with one, not the builtin `.map()` transform - the field wins. A
  field named after a common builtin method name (`map`, `len`, ...) is worth
  a second look for exactly this reason.
- **A malformed `{...}` inside a backtick-interpolated string is a parse
  error upstream; gopherbuzz leaves it as literal text.** Authority:
  GOPHERBUZZ. A typo inside an interpolation placeholder fails loudly
  upstream and fails silently (renders the literal braces) here - review a
  backtick string's interpolated parts as carefully as you would review
  regular code, since gopherbuzz will not catch a malformed one for you.
- **Generics are erased at runtime in gopherbuzz; upstream reifies them.**
  Authority: GOPHERBUZZ. A `::<T>` type argument is parsed and then ignored -
  it exists for the static checker's benefit, not the VM's. Code that
  branches on a generic type argument at runtime cannot work here regardless
  of what upstream would do with it.
- **`pat.replace` replaces only the first match; `pat.replaceAll` replaces
  every match.** Authority: UPSTREAM - gopherbuzz mirrors this faithfully.
  Do not flag it as a bug and do not propose "fixing" `.replace` to replace
  everything; that would be the divergence, not the correction.
- **`str.replace` replaces every occurrence, matching upstream.** Authority:
  UPSTREAM. An earlier gopherbuzz version replaced only the first occurrence;
  that was a bug, and it is fixed. Do not teach or flag the old first-only
  behavior as current.
- **`test "..." {}` is genuine upstream syntax**, present in upstream's own
  test corpus. Authority: UPSTREAM. It is not a gopherbuzz invention - contrast
  with `test` staying bindable as a name (Lens 1), which IS gopherbuzz-only.
- **`assert`, `suite`, `testing`, and `assertcore` have no upstream
  counterpart.** Authority: PORTABILITY. They are gopherbuzz's own test
  surface, not a reimplementation of an upstream module - code that leans on
  their exact API or semantics has no upstream equivalent to fall back to,
  by design, not by omission.

## Running the three lenses

Spawn three `Agent` tool calls **in a single message** (parallel),
`subagent_type: general-purpose`. A subagent cannot invoke the Skill tool, so
it needs the section text, not the skill's name.

Prompt template per subagent:

```text
Read the "Lens: <idiom and style|skeptic and correctness|upstream conformance>"
section of the installed magus-buzz-review skill (.claude/skills/magus-buzz-review/SKILL.md,
or wherever this workspace installed it) and apply it to <target file/dir>.
Establish the surface first (magusfile/spell = always embedded; a standalone
script = check how it is invoked) before applying any strict-mode-derived rule.
Return findings only - file:line, the authority label, what's wrong, severity.
No code. Do not re-explore beyond <target>.
```

If the target is a whole workspace rather than one file, name the entry
points to walk (`magusfile.buzz`, `spells/**/spell.buzz`) rather than leaving
scope open-ended.

## Merging the findings

- **Group by lens.** Keep the three sections separate - they are different
  angles and collapsing them loses which authority backed which finding.
- **Dedupe by `file:line` within a section only.** The same line flagged by
  two lenses for different reasons (idiom's readability angle and
  conformance's portability angle on the same namespace-access mistake, say)
  stays as two findings - that is two lenses agreeing, not one lens repeating
  itself.
- **Combined severity table** at the end, drawn from all three sections.
  A Lens 2 finding with a real side effect (the compound-assignment
  double-evaluation, a swallowed error that matters) outranks a Lens 1
  readability note.
- **Top 1-3 to action first**, opinionated, across all three lenses.

## What this skill does not do

- Magusfile/target/spell contracts - caching, `ctx.needs`, wards, charms, what
  makes an op a service. Use magus-buzz-write.
- Write code or apply fixes. Output is a merged findings report.
- Teach Buzz syntax from scratch. Use magus-buzz-write for that, and point a reader
  there when a finding needs the "how do I write it correctly" answer rather
  than "here is what's wrong".
````


</details>
