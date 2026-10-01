---
title: How gopherbuzz runs Buzz
description: The stages a Buzz program passes through in gopherbuzz, the embedded VM behind every magusfile, from the lexer to the baseline JIT, with the package and the type at each hand-off.
tags: [buzz, gopherbuzz, vm, jit, compiler, lexer, bytecode, interpreter]
---

# How gopherbuzz runs Buzz

Every Buzz program magus runs, a magusfile, a spell or a `magus buzz` script, goes
through [gopherbuzz](https://github.com/egladman/magus/tree/main/libs/gopherbuzz), a
pure-Go Buzz implementation. [Engines](engines.md) covers how magus hosts it; this page
follows one program through it.

## Source to chunk

<!--diagram:buzz-compile-->

`Session.Exec` drives the front end. Every step except the lexer and the chunk passes
lives in the root package, `package buzz`.

| Step              | Where                           | Hands on                                    |
| ----------------- | ------------------------------- | ------------------------------------------- |
| Lexer             | `token.Tokenize`                | `[]token.Token`                             |
| Parse cache       | `ParseCache` (optional)         | the same tokens, keyed by source text       |
| Parser            | `parseModedTracked`             | `*ast.Program`                              |
| Import resolution | `Session.loadFileImports`       | exports bound in the session's `vm.Env`     |
| Checker           | `checkWithPrelude`              | `types.Type` errors as BZZ diagnostics      |
| Compiler          | `CompileWith`                   | `*vm.Chunk`, one per function               |
| Chunk passes      | `vm.FoldConsts`, `FusePeephole` | the same chunk, with superinstructions      |
| Bytecode store    | `Session.SetBytecodeStore`      | a saved chunk, when its files are unchanged |

Import resolution runs before the checker and does more than read files: each imported
module goes through this same pipeline and runs on the VM, so its exports exist when the
importer is checked. A host module arrives instead as a native `vm.Value` with declaration
source for the checker.

The bytecode store is the one superset of upstream Buzz here: it saves a chunk's `.bo`
encoding with every file the chunk was compiled against, including the files its imports
read, and replays it while all of them still hold the same bytes. magus uses it for the
guard's policy files, which a hook loads in a fresh process on every command.

## Chunk to result

<!--diagram:buzz-run-->

gopherbuzz has two tiers, an interpreter and a baseline JIT, and the unit both run is the
chunk. There are no traces and no hotness counter: `VM.Run` offers the chunk it was handed
to the JIT on every run, and the JIT judges each chunk once.

- **Interpreter.** `VM.Exec` is one dispatch switch over word-coded instructions. A value
  is a NaN-boxed 64-bit word, so the operand stack is a `[]uint64` the Go collector never
  scans; a heap object is a handle into one table, and the session's `vm.Owner` releases
  its handles when the session closes. Member and field access go through per-VM inline
  caches.
- **Baseline JIT.** On amd64 and arm64, `depths()` admits a chunk made only of locals,
  numeric constants, arithmetic, comparisons and jumps, and validates every operand;
  `compileJIT` then assembles it with golang-asm into executable pages cached against the
  chunk. Only the chunk `Run` was handed can go native, so a function it calls always
  runs interpreted. A mixed int and float operand is promoted in native code; a non-number
  arriving through `any`, a divide by zero, a NaN result or a float `%` deopts to the
  interpreter at the instruction it stopped on. `BUZZ_JIT=0` turns the tier off.

The [gopherbuzz README](https://github.com/egladman/magus/blob/main/libs/gopherbuzz/README.md)
has the performance design, the platforms the JIT has run on, and where gopherbuzz differs
from upstream Buzz.
