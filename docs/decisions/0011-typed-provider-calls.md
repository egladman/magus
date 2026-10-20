---
title: "ADR 0011: typed provider calls"
order: 11
description: A provider op is a row of one registry, {Op, Req, Resp}. A spell exports it as fun op(target, req) > Resp, the contract check holds it to that signature, and Go calls it through bindings.CallOp, which encodes the request and the Target with generated encoders and decodes the answer strictly. The secret, CI, cache and lifecycle families are on it; review, workspace and the merge queue are not yet.
tags: [adr, decision, spells, providers, buzz, boundary, generated]
status: accepted (in progress)
date: 2026-10-20
---

# ADR 0011: typed provider calls

Each item below carries its own state: _done_ is in the tree, _planned_ is decided and
queued, and _not built_ was weighed and declined.

## Context

A provider spell (cache, CI, secret, lifecycle, workspace, review) exported its ops as
`fun op(target: Target, cb: fun(any)) > any`. magus called the op through
`spells.Driver.Invoke` with an untyped `InvokeRequest.Params`, the spell called `cb(io)` to
have the parameters copied into an empty map, and magus read `InvokeResponse.Data` back
by hand. At 54dfd3947:

- Every family carried its own reader for what came back: `decodeReviewTarget`,
  `decodeReviewComment`, `decodeLifecycle`, `decodeReleaseCycle`, `decodeProvidedProject`,
  three hand-typed `resp.Data.(bool)` checks in the remote cache, and a field-by-field
  `last_green_run` read that tolerated anything.
- The Target a provider op received was built by hand (`targetValue`) and set 4 of
  `types.Target`'s 21 fields.
- `resolve_secret` still accepted a bare `> str` through a shim kept for older providers,
  read from either `Text` or `Data`.
- Nothing checked a provider op's signature: an op typed `> any` loaded, and a wrong
  answer failed at the first call.

The boundary already had the machinery: one registry of Go types (`boundaryTypes`), a
generated encoder per type (`bindgen.Object<T>`), and a strict generated decoder per
inbound type (`decode.Decode<T>`, landed in step 1 of the same plan).

## Decision

### One registry row per provider op

_Done._ `cmd/magus-utils/provider_ops.go` lists every op as
`{Family, Op, Req, Resp, Optional}`. Each request is a RuntimeObject in `boundaryTypes`
and each response is inbound, so both have generated code. The requests and the new
responses live in `types/provider_ops.go` (`Empty`, `Ack`, `SecretRef`, `GreenRun`,
`Strings`, `CIGroup`, `CIGroupEnd`, `CIAnnotation`, `ArtifactRequest`, `PruneRequest`,
`PruneResult`, `LifecycleQuery`) and are mirrored into `magus/spell` under their Go names.
Two families may share an op name (`enabled` for CI and cache) only with one signature,
and the generator refuses one read two ways.

### A spell exports `fun op(target: Target, req: Req) > Resp`

_Done._ The shipped secret, CI, cache and lifecycle providers declare typed signatures
and return objects. `boundarydecode` emits `decode.ProviderFuncs`, one `ContractFunc` per
row with `Params` and `Returns` set, and `checkContract` holds every exported function
named like an op to it: a `cb: fun(any)` parameter or a `> str` resolve_secret is MGS1051
at load.

### Go calls an op through `CallOp`

_Done._ `bindings.CallOp[Req, Resp](ctx, drv, op, target, req)` runs the spell's Buzz
source (`spells.FuncSource`, set by `WithFuncSource` where a Buzz spell is built), calls
the export with `bindgen.ObjectTarget(target)` and `op.Encode(req)`, and decodes the answer
with `op.Decode`. A missing export is `exported == false` with no error, and each caller
says what that means for its family. A malformed answer wraps `errMalformedAnswer`, so the
lifecycle provider can tell it from a throw, which it reads as unreachable.

The per-op values are generated: `bindgen.ProviderOp[Req, Resp]` is the type, and package
`providerops` holds one value per row. They cannot live in bindgen, because a value needs
a decoder from package decode and decode's round-trip test imports bindgen.

### What this deletes

_Done._ `targetValue`, the resolve_secret `> str` path, `spellAnnotator.call`,
`decodeLifecycle`, `decodeReleaseCycle`, and every hand `resp.Data` read in the secret, CI,
cache and lifecycle adapters. `callBuzzSpellFunc` now builds its Target with
`ObjectTarget` too.

### Review, workspace and the merge queue

_Planned._ The review and workspace-provider adapters stay on `Invoke` and `cb` because
four tests outside this change fake them with Go invokers (`cmd/magus/server_test.go`,
`internal/handler/diff/{reading,session}_test.go`, `internal/handler/mcp/diff_test.go`,
`register_test.go`), and a Go invoker has no Buzz source to call. The harness ops stay
there until the neutral harness contract moves them. The merge queue's script provider
keeps `toValue`/`fromValue`/`record`: its types live in `internal/queue/types`, whose package
name collides with `types` in the encoder generator (`buzzValueEmitter.qualify` and
`markPkg` in `cmd/magus-utils/bindings.go` assume `types` or `spells`). Each moves onto
`CallOp` (the queue onto `provider.call`, with no Target) once those files are in reach.

### Doctrine checked

- [No silent defaults](../doctrine.md#no-silent-defaults): an answer the response does not
  declare is an error naming the field, where the old readers ignored unknown keys and read
  a wrong-typed one as absent.
- [Provider I/O is Buzz](../doctrine.md#provider-io-is-buzz): the HTTP and the vendor
  vocabulary stay in the spell; Go only states the shape of what crosses.
- [A copy can be the right answer](../doctrine.md#a-copy-can-be-the-right-answer): the
  per-family meaning of a missing export (always active, unsupported, refuse) stays in
  each caller rather than in a flag on `CallOp`.

## Alternatives

- **Keep `cb` and decode `Data` with the generated decoders.** It lost because the request
  would stay untyped on the Buzz side, and `checkContract` could not check a signature
  whose parameter is `fun(any)`. Reopen if a provider needs a streaming input that one
  request record cannot carry.
- **Declare the ProviderOp values in bindgen, as first planned.** It lost to an import
  cycle in tests: `decode/inbound_list_test.go` (package decode) imports bindgen. Reopen
  if that test moves to package `decode_test`.
- **Have `CallOp` fall back to `Invoke` for a Go driver.** It lost because it would keep
  `Params` and `Data` on the provider path with a second, untyped encoding, which is the
  path this removes. The Go fakes move to Buzz sources instead.

## Consequences

- A third-party provider written against `cb` fails MGS1051 at load, and one returning
  `> str` from resolve_secret does too. magus is pre-1.0; the changelog says so.
- A remote cache provider reads `req.path` where it read `dest` or `src`.
- `InvokeRequest.Params` and `InvokeResponse.Data` stay: the MCP tool adapter and
  `run.go`'s `RecordReturn` read them, and neither is a provider.
- The check cannot pass until `types_generate` and `spells_generate` regenerate the
  encoders, decoders, ProviderOp values and the twelve `magus/spell` mirrors, and
  `./magus` is rebuilt to embed them.

## Open questions

- The review threads op returned the comments that decoded beside an error for the rows
  that did not. A strict list decode is all or nothing. When review moves onto `CallOp`,
  decide whether the per-row tolerance is still wanted now that the spell's signature is
  typed.
