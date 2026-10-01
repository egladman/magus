---
title: Spell contract
generated_from: internal/spell/contract.go
description: Every mgs_ function a spell may export, with the return type magus checks it against. Generated from the spell contract.
tags: [spells, contract, mgs_, reference, MGS1051]
---

# Spell contract

A spell answers magus through exported `mgs_` functions. magus calls each one once, with no arguments, when it loads the spell, before any target runs. Each function must declare exactly the return type below; a different one, `> any`, or an `mgs_` name this page does not list is [MGS1051](codes/magusfile/MGS1051.md).

Only `mgs_getName` is required. Leaving any other function out declares nothing for it.

## mgs_getName

```buzz
export fun mgs_getName() > str
```

Required. The spell's name, the one a magusfile imports it by.

## mgs_listRequiredGlobs

```buzz
export fun mgs_listRequiredGlobs() > [Path]
```

The files every op of the spell reads. They key its cache and pull its project into the affected set.

## mgs_listProvidedGlobs

```buzz
export fun mgs_listProvidedGlobs() > [Path]
```

The files the spell's ops write.

## mgs_listIgnoreDirs

```buzz
export fun mgs_listIgnoreDirs() > [Path]
```

Directories the spell's ecosystem generates (vendor, node_modules, target), which the input-hashing walk prunes. Each Path sets isDir = true.

## mgs_listManifests

```buzz
export fun mgs_listManifests() > [Manifest]
```

The candidate manifests of the spell's ecosystem, in order: the first one present in a project directory is its manifest. Each names the lockfiles it may carry.

## mgs_listScriptRunners

```buzz
export fun mgs_listScriptRunners() > [Command]
```

The argv prefixes that run a script a manifest defines, which doctor reports as MGS1049.

## mgs_getTools

```buzz
export fun mgs_getTools() > {str: Tool}
```

Every binary the spell's ops run, keyed by the bin name a Command names, each with the probe that reads its version into the cache key.

## mgs_getLanguage

```buzz
export fun mgs_getLanguage() > Language
```

The source language the spell adapts: its name, its file extensions and, when the spell can declare it honestly, its comment and string syntax.

## mgs_getSymbolIndexer

```buzz
export fun mgs_getSymbolIndexer() > SymbolIndexer
```

The command that writes the spell's SCIP symbol index. magus runs it as the reserved index op.

## mgs_getSandbox

```buzz
export fun mgs_getSandbox() > Sandbox
```

What the spell's tools need from the host beyond the sandbox's own grants.

## mgs_isOpaque

```buzz
export fun mgs_isOpaque() > bool
```

Whether the spell delegates to a foreign process that manages its own dependency graph, so magus treats the project as a black box.

## mgs_getModeArgs

```buzz
export fun mgs_getModeArgs() > {str: [str]}
```

The args that tell an op apart from other uses of a program with no subcommand, keyed by op name: node runs scripts too, and only --test is node-test.

## mgs_listTargets

```buzz
export fun mgs_listTargets() > {str: fun(Target) Command}
```

The spell's ops, keyed by op name. magus calls each handler once at load, with a null Target, to read the Command it declares, so a handler must not read its Target.

## mgs_listServices

```buzz
export fun mgs_listServices() > {str: fun(Target) Service}
```

The spell's long-running ops, keyed by op name, each read the way a target handler is.
