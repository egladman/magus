---
title: magus-buzz-review
generated_from: internal/agent/skills/magus-buzz-review/SKILL.md
description: "Review Buzz code - a magusfile, a spell, or a standalone .buzz script - across three lenses run in parallel: idiom/style, skeptic/correctness, and upstream-Buzz conformance."
tags: [agents, skills, magus-buzz-review]
skill_full_bytes: 18953
skill_short_bytes: 13065
---

# magus-buzz-review

Review Buzz code - a magusfile, a spell, or a standalone .buzz script - across three lenses run in parallel: idiom/style, skeptic/correctness, and upstream-Buzz conformance. Use when asked to review, audit, or critique a .buzz file or change, or when a finding needs to say whether it holds anywhere Buzz runs (UPSTREAM), only under gopherbuzz (GOPHERBUZZ), or runs here but not upstream (PORTABILITY). Fans out the three lenses via the Agent tool and merges the results, the same shape go-review-ultra uses for Go. Does NOT cover magusfile/target/spell contracts - caching, ctx.needs, wards, charms; use magus-buzz-lang for those.

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
| `agent-skill-version` | `112` |
| `knowledge-schema-version` | `16` |
| `skill-content` | `eaeafed87a30` |
| `skill-variant` | `full` |

The `skill-content` digest covers this skill alone, and both forms below report it: they go stale together, never one silently, and a change to another skill does not move it.

## The two forms

Both are hand-authored from one source body. The short form is the always-loaded primary - the enumeration dropped, the judgment kept, for the most capable readers rather than the least. The full form is its `<name>-full` twin, loaded by name when a reader wants the rationale. The bar above shows how much shorter the primary is; switch between them here to see exactly what it gave up. See [Skills](../../guides/integrations/agents/skills.md) for how to choose.

<article class="landing-tabs">
<header>
<input type="radio" name="magus-buzz-review-variant" id="magus-buzz-review-tab-short" checked>
<label for="magus-buzz-review-tab-short">Short form</label>
<input type="radio" name="magus-buzz-review-variant" id="magus-buzz-review-tab-full">
<label for="magus-buzz-review-tab-full">Full form</label>
</header>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-buzz-review/SKILL.md
```

````markdown
# Reviewing Buzz code

magus-buzz-lang teaches how to WRITE Buzz. This skill REVIEWS it (a magusfile, a spell,
or a standalone `.buzz` script) through three lenses run in parallel.

It covers the LANGUAGE: is the code idiomatic, is it correct, does it run where the
author thinks it runs. Magusfile/target/spell CONTRACTS (caching, `ctx.needs`, wards,
op kinds, charms, command vs service) belong to magus-buzz-lang.

## Authority labels

Every finding in every lens carries one of three labels. "This is wrong" means a
different thing in Buzz depending on the authority behind it.

- `UPSTREAM`: true of Buzz itself, wherever it runs.
- `GOPHERBUZZ`: true only of gopherbuzz, the implementation magus embeds; upstream
  Buzz may lack the construct or resolve it differently.
- `PORTABILITY`: runs fine here under gopherbuzz, but does not parse or behave the
  same against upstream Buzz.

Label every finding from the correctness and conformance lenses. Never invent a
fourth category, and never leave one unlabeled because the answer felt obvious.

## Establish the surface before applying anything

Getting this wrong produces confident false positives. Buzz parses in one of two modes, and most of what the correctness and
conformance lenses check applies to only one:

- **Strict** (upstream parity), `magus buzz <file>`'s default:
  - rejects top-level control flow outside a function
    (`if`/`while`/`for`/`foreach`/`do-until`/`try`/`throw`/`return`/`break`/`continue`/
    a bare block);
  - requires every call argument after the first to be labeled (`name: value`)
    unless it is a bare identifier.
- **Embedded** (relaxed): neither restriction applies. `magus buzz --embedded` opts a
  script in. A magusfile and a spell are embedded UNCONDITIONALLY, with no
  opt-out.

So **a magusfile or a spell file is always embedded.** A top-level `if`, a top-level
`foreach`, an unlabeled second argument: all fine and idiomatic there. Applying
strict-mode rules to a magusfile is not a strict reading; it is a wrong one.

Judge a standalone script by how it is invoked:

- Run by a bare `magus buzz <file>` (no `--embedded`): strict rules apply, and a
  top-level `if` is a genuine defect.
- Strict rules do not apply when it runs by `magus buzz --embedded <file>`, from
  inside another Buzz program (`magus\cmd("buzz", ...)`), or its header comment names
  the surface.
- Unclear: check the CI workflow or wrapper that calls it before flagging a
  strict-mode violation. Its contents alone never prove the mode.

Once you know the mode, PROVE the file parses under it; do not read for it. `magus
buzz --check <file>` checks strict, `--check --embedded` the other. It runs nothing,
takes several paths, and reports every diagnostic instead of stopping at the
first.

A conformance-suite fixture exists to exercise one upstream-vs-gopherbuzz difference
(generics, `match`, a bare `as` cast, `::<T>`, ...). Using that construct is the
fixture doing its job, not a defect.

## Lens: idiom and style

- **Namespace access on an imported module is a backslash, not a dot.** Write
  `fs\readFile(...)`, not `fs.readFile(...)`. Authority: PORTABILITY (see the
  conformance lens for the parse-level reason), and readability. A stray dot reads as a
  method call on a value. Flag it only when the receiver is the imported module
  itself, not a local that shares its name (`path`, `env`, `os`).
- **A flat import (`import "x" as _`) is a finding.** The `_` alias binds no name and
  merges every export of `x` into the importing scope.
  - A call site then reads `escapeAttr(s)`, and nothing in the file says where
    it came from. The reader greps each candidate module, per call site.
  - The module boundary becomes unenforceable. A new export in a flat-imported
    module can silently shadow or collide with a name in every importer.
  - Prefer a named import with namespaced calls (`import "lib/text" as text;`, then
    `text\escapeAttr(s)`). Where an unprefixed name is genuinely wanted, the
    selective form `import escapeAttr, slugify from "lib/text";` states exactly what
    enters the scope.

- **`export fun test(...)` is not a naming smell.** `test` is a contextual soft
  keyword, not a reserved one, so magus's canonical test-target name stays usable.
  Authority: GOPHERBUZZ: the one deliberate place gopherbuzz's reserved-word list is
  a superset of upstream's. Do not flag it or suggest renaming it.
- **A name-shaped parse error may be a reserved word, not a deeper bug.** gopherbuzz
  rejects a reserved word as the name of a variable, parameter, function, or enum
  case. The words: `out`, `from`, `match`, `pat`, `fib`, `rg`, `obj`, `ud`, `zdef`,
  `typeof`, `type`, `protocol`, `static`, `extern`, `double`, `any`, `Function`,
  `int`, `str`, `bool`, `void`.
  - Authority: GOPHERBUZZ. It is gopherbuzz's own enforced list, kept for strict
    parity with upstream's reservations, and not guaranteed identical to upstream's.
  - They stay fine in non-binding position (a map key, a member name, a type name).
    Only binding a NAME to one fails.
- **Object literal vs map literal is a common slip from JSON-familiar authors.**
  `Point{ x = 1 }` (object literal, `=`) and `{"key": value}` (map literal, `:`) look
  alike and are not interchangeable. Flag a literal that mixes the two forms, or
  uses `:` where an object literal was clearly meant. Authority: UPSTREAM.
- **A magusfile carrying logic that wants a test is a finding.** The fix moves that logic into a spell or a sibling
  module; see magus-buzz-lang's "Test what you write".

## Lens: skeptic and correctness

- **A `magus\Context.needs`-adjacent branch on a diagnostic code that names no real
  code.** The idiom for handling a magus failure in Buzz: catch, then branch on
  `e["code"]`, not `e["message"]`.
  - A transposed code (`"MSG3003"` for `"MGS3003"`) or a stale one silently
    never matches. The `catch` block still reads as live error handling while being
    dead code. A code cited only in a comment rots the same way, slower.
  - Authority: GOPHERBUZZ/MAGUS, not upstream. `MGSxxxx` and `BZZxxxx` are magus's
    and gopherbuzz's own diagnostic codes, not a Buzz language concept.
  - Check each `MGS[0-9]{4}` or `BZZ[0-9]{4}` against `docs/reference/codes/`
    and `libs/gopherbuzz/docs/codes/`. A code with no page in either tree is the
    finding. Do not propose typing codes as an enum: the set is open.
- **A force-unwrap (`!`) on a value the type says can be null.** Buzz's nil deref:
  `maybeUser!.name` panics at runtime the moment `maybeUser` is null. Prefer
  `?.`/`??` unless the caller proved non-null a line above. Authority: UPSTREAM.
- **A `catch` that discards `e` without inspecting `code` or `message`, where the
  failure can mean more than one thing.** Swallowing every error the same way is
  how a gate stops being a gate. Authority: UPSTREAM.
- **A compound assignment (`x op= v`) whose target has a side effect**, e.g.
  `f().count += 1`. Authority: GOPHERBUZZ. Upstream evaluates the target ONCE;
  gopherbuzz evaluates it TWICE, so any side effect in `f()` happens twice. This is a real bug under gopherbuzz, not only a
  portability note. Flag it whenever the target is not a bare variable.
- **A `match` treated as exhaustive, or an `obj{...}`/protocol annotation treated as
  enforced.** Authority: GOPHERBUZZ. gopherbuzz's checker enforces neither match
  exhaustiveness, protocol conformance, nor `obj{...}` shape annotations. Check by
  hand that a `match` covers every case its type admits. A
  clean compile proves nothing here.
- **"It compiled" as proof the code is valid upstream Buzz, or "it failed" as proof
  it is not.** Authority: GOPHERBUZZ. gopherbuzz implements a SUBSET of upstream, so
  passing today does not mean upstream would accept it. A clean compile is
  evidence, not verification, in either direction.

## Lens: upstream conformance

- **Namespace access: `\` is the only form upstream recognizes for an imported
  module.** Authority: PORTABILITY. gopherbuzz accepts both for a module
  reference, a superset, not a mirror. `fs\readFile(...)` parses in both;
  `fs.readFile(...)` parses only here.
- **A string is indexed by BYTES; `utf8Len()` is the rune count.** Authority:
  UPSTREAM. `len()`, `sub()`, `indexOf()`, `byte()` and `foreach` all work in bytes,
  matching upstream's builtins; `utf8Len()` is the only codepoint-counting member.
  An accented e is two bytes in UTF-8, so `len()` counts it as 2.

- **A bare `as` cast coerces in gopherbuzz; upstream checks it statically.**
  Authority: GOPHERBUZZ. `3.9 as int` silently truncates to `3` here; upstream
  rejects a cast that cannot hold. `as?` is the real type test in both; prefer it
  when the intent is "test", not "coerce and hope".
- **Compound assignment double-evaluates its target in gopherbuzz; upstream
  evaluates it once.** Authority: GOPHERBUZZ. The correctness lens flags it as a bug
  when the target has a side effect.
- **A declared `!>` error set enforces PRESENCE but not TYPE.** Authority:
  GOPHERBUZZ. Upstream treats `!> ErrType` as a real error set. gopherbuzz checks only
  that a raising call is propagated or caught, never what it raises.
  - Calling a `!> str` function from one declaring no raise is BZZ1006, "call may
    raise but is neither declared with !> nor caught". It is a real and common gate.
  - A function declaring `!> int` may throw a `str` and nothing objects.
  - So never treat a missing `!>` as proof a call cannot raise, and never trust the
    NAMED type. Both were measured against gopherbuzz.
- **An anonymous object field shadows a same-named builtin method.** Authority:
  GOPHERBUZZ. `rec.map` reads the FIELD `map` if the object was built with one, not
  the builtin `.map()`. A field named after a common builtin (`map`, `len`, ...)
  deserves a second look.
- **A malformed `{...}` in a backtick-interpolated string is a parse error upstream;
  gopherbuzz leaves it as literal text.** Authority: GOPHERBUZZ. A placeholder typo
  fails loudly upstream and silently here (it renders the literal braces). Review a
  backtick string's interpolations as carefully as code.
- **Generics are erased at runtime in gopherbuzz; upstream reifies them.**
  Authority: GOPHERBUZZ. A `::<T>` type argument is parsed and ignored: it serves
  the static checker, not the VM. Code that branches on a generic type argument at
  runtime cannot work here.
- **`pat.replace` replaces only the first match; `pat.replaceAll` replaces every
  match.** Authority: UPSTREAM, mirrored faithfully. Do not flag it or propose
  "fixing" `.replace`: that would be the divergence.
- **`str.replace` replaces every occurrence, matching upstream.** Authority:
  UPSTREAM. Never teach or flag the old first-only behavior as current.
- **`test "..." {}` is genuine upstream syntax**, present in upstream's own test
  suite. Authority: UPSTREAM. Contrast `test` staying bindable as a name (the idiom
  lens), which IS gopherbuzz-only.
- **`assert`, `suite`, `testing`, and `assertcore` have no upstream counterpart.**
  Authority: PORTABILITY. They are gopherbuzz's own test surface, not a
  reimplementation of an upstream module.

## Running the three lenses

Spawn three `Agent` tool calls **in a single message** (parallel), `subagent_type:
general-purpose`. A subagent cannot invoke the Skill tool, so it needs the section
text, not the skill's name.

Prompt template per subagent:

```text
Read the "Lens: <idiom and style|skeptic and correctness|upstream conformance>"
section of the installed magus-buzz-review skill (.claude/skills/magus-buzz-review/SKILL.md,
or wherever this workspace installed it) and apply it to <target file/dir>.
Establish the surface first (magusfile/spell = always embedded; a standalone
script = check how it is invoked) before applying any strict-mode-derived rule.
Return findings only: file:line, the authority label, what's wrong, severity.
No code. Do not re-explore beyond <target>.
```

For a whole workspace, name the entry points to walk (`magusfile.buzz`,
`spells/**/spell.buzz`); never leave scope open-ended.

## Merging the findings

- **Group by lens.** Keep the three sections separate: collapsing them loses which
  authority backed which finding.
- **Dedupe by `file:line` within a section only.** One line flagged by two lenses for
  different reasons stays two findings: two lenses agreeing.
- **Combined severity table** at the end, drawn from all three sections. A
  correctness finding with a real side effect (compound-assignment
  double-evaluation, a swallowed error that matters) outranks an idiom note.
- **Top 1-3 to action first**, opinionated, across all three lenses.

## What this skill does not do

- Magusfile/target/spell contracts (caching, `ctx.needs`, wards, charms, what makes
  an op a service). Use magus-buzz-lang.
- Write code or apply fixes. The output is a merged findings report.
- Teach Buzz syntax from scratch. Point a reader to magus-buzz-lang when a finding
  needs "how do I write it correctly", not "here is what's wrong".
````


</section>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-buzz-review-full/SKILL.md
```

````markdown
# Reviewing Buzz code

magus-buzz-lang teaches how to WRITE Buzz. This skill REVIEWS it (a magusfile, a spell,
or a standalone `.buzz` script) through three lenses run in parallel,
the same fan-out-and-merge shape go-review-ultra uses for Go.

It covers the LANGUAGE: is the code idiomatic, is it correct, does it run where the
author thinks it runs. Magusfile/target/spell CONTRACTS (caching, `ctx.needs`, wards,
op kinds, charms, command vs service) belong to magus-buzz-lang and it already covers them; restating them here would only
drift out of sync with it.

## Authority labels

Every finding in every lens carries one of three labels. "This is wrong" means a
different thing in Buzz depending on the authority behind it, and a reader cannot act on an unlabeled finding, only argue about it.

- `UPSTREAM`: true of Buzz itself, wherever it runs.
- `GOPHERBUZZ`: true only of gopherbuzz, the implementation magus embeds; upstream
  Buzz may lack the construct or resolve it differently.
- `PORTABILITY`: runs fine here under gopherbuzz, but does not parse or behave the
  same against upstream Buzz.

Label every finding from the correctness and conformance lenses. Never invent a
fourth category, and never leave one unlabeled because the answer felt obvious.

## Establish the surface before applying anything

This is the single most important step in the whole skill: getting it wrong
produces confident, fluent false positives, because the "violation" did
compile and run. Buzz parses in one of two modes, and most of what the correctness and
conformance lenses check applies to only one:

- **Strict** (upstream parity), `magus buzz <file>`'s default:
  - rejects top-level control flow outside a function
    (`if`/`while`/`for`/`foreach`/`do-until`/`try`/`throw`/`return`/`break`/`continue`/
    a bare block);
  - requires every call argument after the first to be labeled (`name: value`)
    unless it is a bare identifier.
- **Embedded** (relaxed): neither restriction applies. `magus buzz --embedded` opts a
  script in. A magusfile and a spell are embedded UNCONDITIONALLY, with no
  opt-out; every code path that loads one passes the embedded option before
  the author's own code runs, so there is no author-visible switch to get wrong.

So **a magusfile or a spell file is always embedded.** A top-level `if`, a top-level
`foreach`, an unlabeled second argument: all fine and idiomatic there. Applying
strict-mode rules to a magusfile is not a strict reading; it is a wrong one.

Judge a standalone script by how it is invoked, not by
guessing from its shape:

- Run by a bare `magus buzz <file>` (no `--embedded`): strict rules apply, and a
  top-level `if` is a genuine defect.
- Strict rules do not apply when it runs by `magus buzz --embedded <file>`, from
  inside another Buzz program (`magus\cmd("buzz", ...)`), or its header comment names
  the surface.
- Unclear: check the CI workflow or wrapper that calls it before flagging a
  strict-mode violation. A script that happens to have no
  top-level control flow and no unlabeled second argument is ALSO valid
  embedded Buzz, so its contents alone never prove which mode the author
  wrote it for; only the invocation does.

Once you know the mode, PROVE the file parses under it; do not read for it. `magus
buzz --check <file>` checks strict, `--check --embedded` the other. It runs nothing,
takes several paths, and reports every diagnostic instead of stopping at the
first, so a strict-mode finding either reproduces as a
diagnostic naming the line or was never there. A file whose job is a side effect
cannot be checked any other way, since running it exercises one path and says
nothing about the rest.

A conformance-suite fixture exists to exercise one upstream-vs-gopherbuzz difference
(generics, `match`, a bare `as` cast, `::<T>`, ...). Using that construct is the
fixture doing its job, not a defect.

## Lens: idiom and style

What reads as Buzz house style versus what merely parses.

- **Namespace access on an imported module is a backslash, not a dot.** Write
  `fs\readFile(...)`, not `fs.readFile(...)`. Authority: PORTABILITY (see the
  conformance lens for the parse-level reason), and readability. `path\join(a, b)`
  and `record.join(a, b)` parse to visually identical postfix chains; only the token
  tells "call into an imported module" from "call a method on this value". A stray
  dot reads as a value method call to anyone who has not memorized which names are
  modules.
  FALSE-POSITIVE GUARD: a local variable, field, or ctx member that happens
  to share a module's bare name (`path`, `env`, `os` are common ones) uses a
  dot correctly. `path.endsWith(".buzz")` where `path` is a local `str` is
  ordinary member access, not a namespace-access mistake, even though `path`
  is also an importable module name. Only flag the dot when the receiver is
  the actual imported module identifier itself.
- **A flat import (`import "x" as _`) is a finding.** The `_` alias binds no name and
  merges every export of `x` into the importing scope.
  - A call site then reads `escapeAttr(s)`, and nothing in the file says where
    it came from. The reader greps each candidate module, per call site.
  - The module boundary becomes unenforceable. A new export in a flat-imported
    module can silently shadow or collide with a name in every importer.
  - Prefer a named import with namespaced calls (`import "lib/text" as text;`, then
    `text\escapeAttr(s)`). Where an unprefixed name is genuinely wanted, the
    selective form `import escapeAttr, slugify from "lib/text";` states exactly what
    enters the scope.
  Note the flat and selective forms are BOTH excluded from
  unused-import tracking (BZZ3001): a flat import has no bound name to mark
  unused, so an `as _` that has stopped being needed is never reported.
  Weigh the finding by blast radius, not by count: a leaf module flat-importing
  one helper is a nit, while a tree where every file flat-imports every other is
  a single structural finding, not one per line.
- **`export fun test(...)` is not a naming smell.** `test` is a contextual soft
  keyword, not a reserved one, so magus's canonical test-target name stays usable.
  Authority: GOPHERBUZZ: the one deliberate place gopherbuzz's reserved-word list is
  a superset of upstream's. Do not flag it or suggest renaming it.
- **A name-shaped parse error may be a reserved word, not a deeper bug.** gopherbuzz
  rejects a reserved word as the name of a variable, parameter, function, or enum
  case. The words: `out`, `from`, `match`, `pat`, `fib`, `rg`, `obj`, `ud`, `zdef`,
  `typeof`, `type`, `protocol`, `static`, `extern`, `double`, `any`, `Function`,
  `int`, `str`, `bool`, `void`.
  - Authority: GOPHERBUZZ. It is gopherbuzz's own enforced list, kept for strict
    parity with upstream's reservations, and not guaranteed identical to upstream's.
  - They stay fine in non-binding position (a map key, a member name, a type name).
    Only binding a NAME to one fails.
- **Object literal vs map literal is a common slip from JSON-familiar authors.**
  `Point{ x = 1 }` (object literal, `=`) and `{"key": value}` (map literal, `:`) look
  alike and are not interchangeable. Flag a literal that mixes the two forms, or
  uses `:` where an object literal was clearly meant. Authority: UPSTREAM.
- **A magusfile carrying logic that wants a test is a finding.** A
  magusfile is declarative configuration; a test of it tests your
  configuration, not your logic. The fix moves that logic into a spell or a sibling
  module; see magus-buzz-lang's "Test what you write".

## Lens: skeptic and correctness

Bugs and silent failures, not style. Read every function assuming it has one.

- **A `magus\Context.needs`-adjacent branch on a diagnostic code that names no real
  code.** The idiom for handling a magus failure in Buzz: catch, then branch on
  `e["code"]`, not `e["message"]`.
  - A transposed code (`"MSG3003"` for `"MGS3003"`) or a stale one silently
    never matches. The `catch` block still reads as live error handling while being
    dead code. A code cited only in a comment rots the same way, slower.
  - Authority: GOPHERBUZZ/MAGUS, not upstream. `MGSxxxx` and `BZZxxxx` are magus's
    and gopherbuzz's own diagnostic codes, not a Buzz language concept.
  CHECK: any string matching `MGS[0-9]{4}` or `BZZ[0-9]{4}` in a `.buzz`
  file, in code or in a comment, against the documented set: `docs/reference/codes/`
  (organized by family: auth, charms, knowledge, magusfile, outputref, race,
  sandbox, services; one page per `MGSxxxx` code) for magus's own codes, and
  `libs/gopherbuzz/docs/codes/` (flat, one page per `BZZxxxx` code) for
  gopherbuzz's. A code that names no file in either tree is the finding.
  A code cited as DATA, a fixture or table row that names a real code as an
  example, rather than comparing against a caught error, is not a defect
  just because it is not itself a comparison; check whether the code exists,
  not whether the surrounding line is a comparison.
  Do not turn this into "type diagnostic codes as an enum": `Arg.Enum` scopes
  itself to a parameter whose values are a closed set, this set is not closed
  (it grows every release across two independently-versioned namespaces), the
  real read site is a map key off a caught error rather than a function
  parameter, and an enum case an older magus release lacks fails to LOAD;
  worse than a string that silently never matches. A closed set like a sign
  algorithm name is what `Arg.Enum` is for; a diagnostic code is not that
  shape.
- **A force-unwrap (`!`) on a value the type says can be null.** Buzz's nil deref:
  `maybeUser!.name` panics at runtime the moment `maybeUser` is null. Prefer
  `?.`/`??` unless the caller proved non-null a line above. Authority: UPSTREAM.
- **A `catch` that discards `e` without inspecting `code` or `message`, where the
  failure can mean more than one thing.** Swallowing every error the same way is
  how a gate stops being a gate: the failure go-review-skeptic flags for a Go
  `default` case that silently no-ops. Authority: UPSTREAM.
- **A compound assignment (`x op= v`) whose target has a side effect**, e.g.
  `f().count += 1`. Authority: GOPHERBUZZ. Upstream evaluates the target ONCE;
  gopherbuzz evaluates it TWICE, so any side effect in `f()` happens twice (a
  mutation, a log line, a counter). This is a real bug under gopherbuzz, not only a
  portability note. Flag it whenever the target is not a bare variable.
- **A `match` treated as exhaustive, or an `obj{...}`/protocol annotation treated as
  enforced.** Authority: GOPHERBUZZ. gopherbuzz's checker enforces neither match
  exhaustiveness, protocol conformance, nor `obj{...}` shape annotations. Check by
  hand that a `match` covers every case its type admits, as go-review-skeptic checks a Go type switch for a missing `default`. A
  clean compile proves nothing here.
- **"It compiled" as proof the code is valid upstream Buzz, or "it failed" as proof
  it is not.** Authority: GOPHERBUZZ. gopherbuzz implements a SUBSET of upstream, so
  passing today does not mean upstream would accept it. Several of upstream's own
  compile-error fixtures compile clean under gopherbuzz. A clean compile is
  evidence, not verification, in either direction.

## Lens: upstream conformance

Named divergences between gopherbuzz and upstream Buzz. Each is a real,
observed behavior difference, not a hypothetical; use them to judge whether
code an author believes is "just Buzz" survives contact with
upstream, and to stop a gopherbuzz-only behavior from being taught as if it
were the language.

- **Namespace access: `\` is the only form upstream recognizes for an imported
  module.** Authority: PORTABILITY. Upstream's parser gives backslash a dedicated
  production for resolving a name against an import; the bare dot is only the
  general postfix member operator on VALUES. gopherbuzz accepts both for a module
  reference, a superset, not a mirror. `fs\readFile(...)` parses in both;
  `fs.readFile(...)` parses only here.
- **A string is indexed by BYTES; `utf8Len()` is the rune count.** Authority:
  UPSTREAM. `len()`, `sub()`, `indexOf()`, `byte()` and `foreach` all work in bytes,
  matching upstream's builtins; `utf8Len()` is the only codepoint-counting member.
  An accented e is two bytes in UTF-8, so `len()` counts it as 2.
  This is worth knowing because gopherbuzz USED to index runes, and
  code written against that reads plausibly either way. A loop slicing with
  `sub()` and bounding with `len()` was consistent under both models, so it does
  not announce the change; what moves is any index arithmetic that assumed one
  character was one position. Reach for `utf8Len()` only when the question is
  genuinely "how many characters", which is rarer than it looks; a byte count
  is what a digest, an encoding, or a wire format wants.
- **A bare `as` cast coerces in gopherbuzz; upstream checks it statically.**
  Authority: GOPHERBUZZ. `3.9 as int` silently truncates to `3` here; upstream
  rejects a cast that cannot hold. `as?` is the real type test in both; prefer it
  when the intent is "test", not "coerce and hope".
- **Compound assignment double-evaluates its target in gopherbuzz; upstream
  evaluates it once.** Authority: GOPHERBUZZ. The correctness lens flags it as a bug
  when the target has a side effect; here it explains why upstream does not
  misbehave the same way.
- **A declared `!>` error set enforces PRESENCE but not TYPE.** Authority:
  GOPHERBUZZ. Upstream treats `!> ErrType` as a real error set. gopherbuzz checks only
  that a raising call is propagated or caught, never what it raises.
  - Calling a `!> str` function from one declaring no raise is BZZ1006, "call may
    raise but is neither declared with !> nor caught". It is a real and common gate: it is what a script invoked by `magus buzz`
    trips on when it calls something like `fs\listDir` without declaring
    `!>`.
  - A function declaring `!> int` may throw a `str` and nothing objects, so the named type is documentation while the arrow
    itself is checked.
  - So never treat a missing `!>` as proof a call cannot raise, and never trust the
    NAMED type. Both were measured against gopherbuzz; this bullet
    previously said the whole annotation was "parsed and thrown away, nothing
    enforces it", which sent reviewers straight past every BZZ1006-class defect.
- **An anonymous object field shadows a same-named builtin method.** Authority:
  GOPHERBUZZ. `rec.map` reads the FIELD `map` if the object was built with one, not
  the builtin `.map()`. A field named after a common builtin (`map`, `len`, ...)
  deserves a second look.
- **A malformed `{...}` in a backtick-interpolated string is a parse error upstream;
  gopherbuzz leaves it as literal text.** Authority: GOPHERBUZZ. A placeholder typo
  fails loudly upstream and silently here (it renders the literal braces). Review a
  backtick string's interpolations as carefully as code.
- **Generics are erased at runtime in gopherbuzz; upstream reifies them.**
  Authority: GOPHERBUZZ. A `::<T>` type argument is parsed and ignored: it serves
  the static checker, not the VM. Code that branches on a generic type argument at
  runtime cannot work here.
- **`pat.replace` replaces only the first match; `pat.replaceAll` replaces every
  match.** Authority: UPSTREAM, mirrored faithfully. Do not flag it or propose
  "fixing" `.replace`: that would be the divergence.
- **`str.replace` replaces every occurrence, matching upstream.** Authority:
  UPSTREAM. An earlier gopherbuzz replaced only the first occurrence; that bug is
  fixed. Never teach or flag the old first-only behavior as current.
- **`test "..." {}` is genuine upstream syntax**, present in upstream's own test
  suite. Authority: UPSTREAM. Contrast `test` staying bindable as a name (the idiom
  lens), which IS gopherbuzz-only.
- **`assert`, `suite`, `testing`, and `assertcore` have no upstream counterpart.**
  Authority: PORTABILITY. They are gopherbuzz's own test surface, not a
  reimplementation of an upstream module. Code leaning on their exact API has no
  upstream equivalent, by design.

### If you are reviewing gopherbuzz's own implementation

The above applies to reviewing a workspace's magusfile or spells; skip this
if that is all you are doing. If the change under review touches gopherbuzz
itself, do not quote a conformance score from a code comment; a comment has
carried a wrong number before, and a second stale number lived in the test
file at the same time. The one authoritative count is the line count of the
checked-in upstream-behavior allowlist the conformance test enforces in both
directions: it fails if a passing file regresses, and it also fails if a
newly-passing file is not added to the list, so the list can only be as stale
as the last test run.

## Running the three lenses

Spawn three `Agent` tool calls **in a single message** (parallel), `subagent_type:
general-purpose`. A subagent cannot invoke the Skill tool, so it needs the section
text, not the skill's name.

Prompt template per subagent:

```text
Read the "Lens: <idiom and style|skeptic and correctness|upstream conformance>"
section of the installed magus-buzz-review skill (.claude/skills/magus-buzz-review/SKILL.md,
or wherever this workspace installed it) and apply it to <target file/dir>.
Establish the surface first (magusfile/spell = always embedded; a standalone
script = check how it is invoked) before applying any strict-mode-derived rule.
Return findings only: file:line, the authority label, what's wrong, severity.
No code. Do not re-explore beyond <target>.
```

For a whole workspace, name the entry points to walk (`magusfile.buzz`,
`spells/**/spell.buzz`); never leave scope open-ended; an unscoped subagent re-derives the workspace layout three
times instead of once.

## Merging the findings

- **Group by lens.** Keep the three sections separate: collapsing them loses which
  authority backed which finding.
- **Dedupe by `file:line` within a section only.** One line flagged by two lenses for
  different reasons stays two findings: two lenses agreeing. The common case is idiom's readability and conformance's portability on the same
  namespace-access mistake.
- **Combined severity table** at the end, drawn from all three sections. A
  correctness finding with a real side effect (compound-assignment
  double-evaluation, a swallowed error that matters) outranks an idiom note.
- **Top 1-3 to action first**, opinionated, across all three lenses.

## What this skill does not do

- Magusfile/target/spell contracts (caching, `ctx.needs`, wards, charms, what makes
  an op a service). Use magus-buzz-lang.
- Write code or apply fixes. The output is a merged findings report.
- Teach Buzz syntax from scratch. Point a reader to magus-buzz-lang when a finding
  needs "how do I write it correctly", not "here is what's wrong".
````


</section>

</article>
