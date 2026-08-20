# Improving the generated daemon API reference

Status: SHIPPED. Branch `feat/protobuf-docs-generator-237731`.
Subject: `cmd/magus-protodocs/main.go` and the pages it writes to `docs/reference/api/`.

Phases 1, 2, 3, and 4 below all landed in place against the existing generator (Markdown
output, same CLI, same `docs/magusfile.buzz` wiring) - see "What actually shipped" for the
final shape, which differs from the plan in two ways worth reading before the rest of this
document: the internals were rebuilt on `protodesc`/`protoreflect` rather than hand-rolled
descriptorpb path-walking, and pages nest by `.proto` path rather than staying flat.

## Considered and deliberately deferred: a standalone protoc plugin

Mid-implementation, the option of rewriting this as a real protoc-plugin-protocol binary
(`protoc-gen-magus-doc`, using `google.golang.org/protobuf/compiler/protogen`, emitting
semantic HTML instead of Markdown, wired into `proto/buf.gen.yaml` as a `local:` plugin
the same way `protoc-gen-go` is) was explored in real depth - protogen availability,
whether `buf generate` threads `SourceCodeInfo` to local plugins (it does; the Image it
builds carries it, and this generator's whole comment/line-number pipeline is proof), and
how a raw-HTML page would flow through the site's goldmark pipeline (`WithUnsafe` passes
raw HTML through, but `WithAutoHeadingID` does not touch it, so cross-page anchors would
need computing by hand exactly as they already are here). The direction is sound and
worth revisiting for real external distribution, but it was explicitly parked for this
pass: "keep this in Buzz for now... we can explore this further down the line later."
Nothing from that exploration was built; this document records it so it is not
re-litigated from scratch next time.

## What actually shipped, beyond this document's original phases

- **protodesc/protoreflect, not hand-rolled descriptorpb.** `newAPI` resolves the
  descriptor set once via `protodesc.NewFiles` and walks real `protoreflect` descriptors.
  This replaced the planned "widen commentIndex into a location index" (1a) outright:
  `FileDescriptor.SourceLocations().ByDescriptor(desc)` returns exact start/end lines
  directly, `FieldDescriptor.IsMap()`/`MapKey()`/`MapValue()` replaced the two-pass
  synthetic-map-entry bookkeeping, and `OneofDescriptor.IsSynthetic()` replaced the
  proto3-optional special case. `buf.validate` constraints (2a) read via
  `proto.GetExtension` against `fd.Options()`, generically over the `FieldRules`
  message via `protoreflect.Message.Range` rather than one case per constraint kind - a
  protovalidate rule this schema starts using tomorrow renders with no code change here.
- **Pages nest by `.proto` path**, not by service name: `token.md` became
  `token/v1/token.md`, mirroring `proto/magus/token/v1/token.proto` (minus the redundant
  `magus/` prefix every package shares). This was a mid-session redirect ("I would want
  the documentation to reflect the protobuf pathing") and it subsumed phase 3's page-
  hierarchy open question for free: a package with no service (`magus.query.v1`,
  `magus.graph.v1`) simply gets its own page at its own path, with no separate
  "package page" naming scheme to invent. Cross-page links are computed as real relative
  paths (`relLink`) since pages no longer share one flat directory.
  - **No redirect from the old flat URLs.** The natural instinct - add
    `aliases: [reference/api/token]` - collides: the site synthesizes a section-index
    page for every directory that contains pages with no landing page of its own
    (`Site.renderSubtree` in `docs/engine/types.buzz`), and the new nested tree makes
    `reference/api/token/` exactly such a directory. `writeAliases`' collision guard
    correctly refuses to let a redirect stub shadow that real page. No redirect was
    needed anyway: `abandonedLinkCheck`'s `checkPathExists` only requires the old URL to
    resolve to *something*, and the auto-generated section index (which lists the one
    real page beneath it) already satisfies that - confirmed by running the full
    `magus run generate docs` link-integrity gate clean.
- **`Used by` reverse references** (3c) shipped as designed: `computeUsedBy` walks every
  method's request/response transitively once, globally, and every message/enum page
  renders which methods reach it.
- **Deprecation** (2c) is real code, not speculative: `isDeprecated` reads `.Options()`
  generically via a small `deprecatable` interface satisfied by every descriptorpb
  `*Options` type, applied at message/field/enum/enum-value/service/method level. Nothing
  in this schema exercises it yet, so it is unverified against a live example - the same
  caveat the original plan already carried.
- **The example request body** (4b) is a real shallow JSON skeleton (top-level scalar and
  enum fields, `protoreflect.FieldDescriptor.JSONName()` for wire casing), not `{}`. It
  does not attempt to satisfy every field's `buf.validate` constraint (a `string.pattern`
  rule still needs a matching value) - building a constraint-satisfying value synthesizer
  was scoped out as disproportionate to the payoff for an illustrative example; the page
  says so explicitly rather than silently overclaiming.

The pages are wired into `docs/magusfile.buzz:216-230` (`content_generate`), reading
`proto/gen/descriptor.binpb` as a declared input; the output glob there is now
`reference/api/**/*.md` (recursive) rather than `reference/api/*.md`, to match the nested
tree - the flat glob was the one thing that silently kept working through local testing
right up until the drift-gate's declared-outputs accounting would have gone stale. The
generator's package comment no longer says "run it manually"; it names the real target.

## 1. What the field does

Seven generators surveyed. Two are worth learning from; the rest confirm the baseline.

| Tool | Output | What it does that we do not |
| --- | --- | --- |
| [protoc-gen-doc](https://github.com/pseudomuto/protoc-gen-doc) | HTML, Markdown, DocBook, JSON, custom Go template | Per-field `Label`, `LongType`, `FullType`, `IsMap`, `IsOneof`, `OneofDecl`, `DefaultValue`, and an `Options` map on every entity. Ships a [scalar-type table](https://pkg.go.dev/github.com/pseudomuto/protoc-gen-doc) mapping each proto type to C++/C#/Go/Java/PHP/Python/Ruby. Has a `camel_case_fields` switch because the JSON name is not the proto name. |
| [Buf Schema Registry](https://buf.build/docs/bsr/documentation/) | Hosted | The UX benchmark. Module landing page -> package page -> entity. Sidebar index grouped by kind. A copyable anchor on every entity header. Filenames link into a source browser with cross-file navigation. Deprecation renders as a banner. Built-in AND custom options render inline. Comments render as CommonMark/GFM (and Mermaid). Search with click-through on types. |
| [protoc-gen-connect-openapi](https://github.com/sudorandom/protoc-gen-connect-openapi) | OpenAPI 3 | The precedent for validation: maps protovalidate constraints onto OpenAPI keywords (`int32.gte` -> `minimum`) and appends custom CEL to the description. |
| [Sabledocs](https://blog.markvincze.com/introducing-sabledocs/) | Static site | Covers gRPC services as well as data contracts; author states `oneof` and option annotations are not implemented. |
| [protoc-gen-apidocs](https://github.com/tmc/protoc-gen-apidocs) | Markdown | Markdown-first, cleaner default than protoc-gen-doc's. |
| [sourcegraph/prototools](https://github.com/sourcegraph/prototools) | HTML | Go `html/template` driven. |
| [proto2asciidoc](https://github.com/productsupcom/proto2asciidoc) | AsciiDoc | Nothing we need. |

The consistent finding: everyone renders the schema's *structure*. Only BSR and
protoc-gen-connect-openapi render the schema's *constraints and provenance* - which is
exactly where ours is thin.

## 2. Where ours stands

Every gap below was checked against this tree, not inferred.

### G1 - No source links (the stated ask)

No page links to a `.proto`. `token.md` says only "defined in
`proto/magus/token/v1/token.proto`", as plain text.

The data is already in the file we read. `SourceCodeInfo.Location` carries a `span`
alongside the comments the generator already indexes. Decoded from the committed
`proto/gen/descriptor.binpb`:

```text
path [6, 0]        span [45, 0, 53, 1]    -> token.proto:46  `service TokenService {`
path [6, 0, 2, 0]  span [48, 2, 65]       -> token.proto:49  `rpc ListTokens(...)`
```

Line is zero-indexed; `span[0] + 1` is the source line, verified against the file. The
paths are the same ones `commentIndex` already builds, so every service, method, message,
field, enum, and enum value has an exact line for free.

The house pattern is `internal/docs.RepoBlob` plus `docs.SourceURL`
(`internal/docs/source.go:42`), used by `cmd/magus-spelldocs/main.go:286`. Reuse
`RepoBlob`; do NOT reuse `SourceURL` - it finds a line by text-matching the file, which is
a guess, and here we have the exact number.

### G2 - Validation constraints are invisible

Thirteen `buf.validate` sites across three files. None appear in the docs:

- `activity.proto:137` - `page_size` must be `0..1000`
- `activity.proto:148` - `ref` must match `^[a-z]{2,8}[0-9a-f]+$`
- `viewer.proto:137` - the `Selector` oneof is `required`
- `viewer.proto:169` - `page_size` must be `0..5000`
- `token.proto:107` - `identifier` min length 1

The daemon rejects requests for reasons the published reference does not state. This is a
correctness gap, not polish.

It is cheap to close: `go.mod:6` already requires
`buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go` directly, and the descriptor
set is self-contained (it carries `google/protobuf/descriptor.proto` and
`buf/validate/validate.proto`, confirmed by decoding the file list). Importing the
generated package registers the extension in `protoregistry.GlobalTypes`, which is what
`proto.Unmarshal` already resolves against - so `proto.GetExtension(fd.GetOptions(),
validate.E_Field)` should work with no resolver plumbing. Prove that with a test before
building a renderer on it.

### G3 - Orphan packages

`reachableFrom` (`main.go`) walks only from service method signatures. `magus.graph.v1`
declares no service and is imported by no other proto, so `Graph` and everything in
`graph.proto` appears on **zero** pages.

It is not dead. `internal/handler/graph/handler.go` serves `GET /api/v1/graph` and encodes
with `protojson`, and `graph.proto`'s own package comment names it "the versioned wire
contract for the knowledge-graph output the daemon serves to the browser Graph Explorer".
A published contract with no published documentation.

`magus.query.v1` has the mirror problem - see G4.

### G4 - Shared types duplicated, with no canonical home

`TimeRange` and `StringMatch` live in `magus.query.v1`, which exists specifically to hold
shared primitives. They render in full on **both** `activity.md` and `viewer.md`. Two
copies, two anchors, no link between them, and no page that owns either.

### G5 - No cross-links between types

"Takes `ListTokensRequest`, returns `ListTokensResponse`" is plain text. So is every
`Type` cell (`repeated TokenInfo`). Every generator in the table links these. A reader on
`viewer.md` looking at `magus.query.v1.TimeRange` has no way to reach it but scrolling.

### G6 - `reserved` and `deprecated` are dropped

`token.proto:90` reserves numbers 4 and 6 and the names `created`, `last_used`;
`status.proto:24` reserves 3 / `magus_version`; `viewer.proto:26` reserves 3 and 5. The
rendered field-number column therefore shows unexplained gaps. `DescriptorProto` and
`EnumDescriptorProto` expose `ReservedRange` and `ReservedName` directly.

Nothing in the tree is `deprecated` yet, so that half has no observation behind it - build
it against a fixture, not a tree grep, and keep it small.

### G7 - The JSON body is not the proto schema

This is the sharpest client trap on the site and it is completely unstated:

- protojson emits **lowerCamelCase** by default. The tables say `page_size`; a Connect
  client sends `pageSize`. protoc-gen-doc carries a `camel_case_fields` option precisely
  for this.
- `int64`/`uint64`/`fixed64` encode as JSON **strings**. `metrics.proto` alone has 33
  int64/bytes lines.
- `bytes` is base64, `Timestamp` is an RFC3339 string, `Duration` is a `"1.5s"` string,
  enums serialize as the value NAME.

The index page's single `curl` example sends `-d '{}'`, so it never exercises any of this.

### G8 - Comments are flattened and over-escaped

`clean()` (`main.go`) joins every line with a space and runs
`strings.NewReplacer("|", "\\|", "_", "\\_")` over the whole comment. Consequences: no
paragraph breaks, no lists, no links, and any `_` inside a backtick span renders as a
literal `\_`. The schema's comments are long and structured - `TokenScope`'s
`TOKEN_SCOPE_OPERATOR` value is a full paragraph crammed into one table cell. BSR renders
CommonMark here.

### G9 - No global type index, and no per-method examples

There is no "all messages" list. The only way to find `Event` is to guess which service
page carries it. Per method there is no request or response shape beyond the type name.

### G10 - Streaming is named but not explained

Three server-streaming methods (`StreamMetrics`, `StreamStatus`, `StreamEvents`). Each
page prints "server streaming" and stops. The only worked example on the site is unary
`curl`.

### Already fine - do not "fix" these

- **On-page navigation.** `docs/engine/nav.buzz:90` (`renderTocList`) already builds an
  "On this page" list from h2/h3, so methods, messages, and enums nest correctly under
  their sections. The gap is cross-linking, not TOC.
- **Heading anchors.** goldmark emits `id` attributes; no page currently has two headings
  with the same text. The collision (a method named the same as a message) is latent, not
  present. Guard it if the code is being touched anyway; do not restructure for it.
- **Page pruning, byte-stability, comment sourcing.** `writeAll` already prunes stale
  pages, renders before writing, and reads trailing comments (161 of this schema's field
  comments are trailing). Those decisions are correct and documented.

## 3. Plan

Four phases. Each unit names its own check. Phases 1 and 2 are worth doing regardless;
phase 3 carries a URL decision that needs a call before anything is written.

### Phase 1 - source links and the index they need

**1a. Widen the comment index into a location index.**
`commentIndex` returns `map[string]string`. Make the value a small struct carrying the
prose and the start line (and end line when the span has four elements). Same paths, same
single pass. Every `c.at(...)` call site gains a line for free.
*Check:* a unit test asserting `[6,0]` on `token.proto` resolves to line 46.

**1b. Emit a source link per entity.**
Path is `proto/` + `FileDescriptorProto.GetName()`; base is `docs.RepoBlob`. Render
`[token.proto:46](.../proto/magus/token/v1/token.proto#L46)` after each service, method,
message, and enum heading. Field-level and enum-value-level links go in the table row only
if they do not wreck the column widths - decide by looking at the rendered page, not up
front.
*Check:* `magus run generate docs` produces pages whose every link 404-checks; the site's
existing asset-integrity gate covers internal links, so external blob URLs need a spot
check by hand.

**1c. Correct the stale package comment** in `cmd/magus-protodocs/main.go` (it is wired
into `content_generate`, not run manually).

Honest caveat to record in the code, not just here: `RepoBlob` is pinned to `main`
(`internal/docs/source.go:10`) while the descriptor is built from the working tree. On a
branch that moves a `.proto`, the committed link points at `main`'s line numbering until
the merge lands. Both halves land in the same commit, so they converge at merge. This is
already true of `docs.SourceURL`; it is a note, not a redesign.

### Phase 2 - correctness: say what the daemon actually enforces

**2a. Render protovalidate constraints.** Prove `proto.GetExtension` resolves the
extension from the descriptor set first (one test). Then render short phrases - `0-1000`,
`matches ^out[0-9a-f]+$`, `required`, `min length 1` - appended to the existing
`_optional_` / `_one of X_` note convention in the Description cell rather than as a fifth
column. Oneof-level `(buf.validate.oneof).required` renders on the oneof, not the field.
CEL expressions we cannot phrase get printed verbatim, the way
protoc-gen-connect-openapi does.
*Check:* `viewer.md` states the `^out[0-9a-f]+$` pattern and the `0..5000` bound;
`token.md` states min length 1.

**2b. Render `reserved`.** One line under each affected field or value table: "Reserved:
4, 6; `created`, `last_used`." Explains the gaps in the number column.
*Check:* `token.md`, `status.md`, `viewer.md` each carry their reserved line.

**2c. Render deprecation.** `options.deprecated` at file, message, field, service, method,
enum, and value level, as a leading `**Deprecated.**`. Nothing in the tree exercises it, so
this needs a fixture descriptor, not a tree observation. Keep it to the smallest thing that
works.

**2d. Document packages that own no service.** `magus.graph.v1` currently appears nowhere.
Emit a page per package for packages with messages but no service, listed on the index
alongside the services, and say on it which route serves the contract (`GET
/api/v1/graph`). This also gives `magus.query.v1` a home, which phase 3 needs.
*Check:* `Graph` is reachable from the index in one click.

### Phase 3 - cross-linking and one canonical home per type

Needs a decision first (see Open questions): whether service pages stay the top-level unit
or the hierarchy becomes index -> package -> service, BSR-style. The second is better and
breaks URLs; the site has an `aliases` mechanism in `docs.Frontmatter` and a
`retired.urls.lock`, so the break is manageable but is a real cost.

Assuming service pages stay:

**3a. One canonical page per type.** A type reachable from exactly one service renders on
that service's page. A type reachable from more than one renders on its package page
(phase 2d) and is *linked*, not copied, from each service page. Kills the `TimeRange`
duplication.

**3b. Link every type reference.** Method input/output and every `Type` cell become links
to the canonical anchor. Guard against a method name colliding with a message name on one
page while the anchor logic is being written.

**3c. Reverse references.** Under each message, "Used by: `ListEvents` (request),
`StreamEvents` (request)". Computed from the same walk `reachableFrom` already does. This
is the single highest-value BSR feature we lack and it is nearly free.

**3d. Package comments.** Every `.proto` has a substantial package comment at
`SourceCodeInfo` path `[2]` - `viewer.proto`'s is twelve lines describing the whole event
model - and it renders nowhere. It is the natural body of a package page.

### Phase 4 - make it callable

**4a. A JSON mapping section on the index.** lowerCamelCase field names, int64 as string,
bytes as base64, Timestamp/Duration as strings, enums by name. Short, one place, linked
from every page. This is the highest ratio of reader-pain-removed to work on the list.

**4b. Per-method example.** A `curl` with a JSON skeleton synthesized from the request
message, in protojson casing, honouring the validation bounds from 2a so the example is
actually accepted. Keep the generated skeleton shallow (one level, scalars only) rather
than trying to be complete.

**4c. Streaming guidance.** One section on the index covering how the three streaming
methods are called over Connect, linked from each streaming method.

**4d. A scalar-type table**, protoc-gen-doc style, if 4a leaves anything unanswered. Lowest
priority; possibly redundant once 4a exists.

## 4. Deliberately not doing

- **Replacing the generator with protoc-gen-doc or Sabledocs.** Ours already does things
  they do not: it prunes stale pages, walks the type graph transitively, folds map entries
  back to `map<k,v>`, resolves proto3 optional's synthetic oneof, and emits this site's
  frontmatter. Adopting a general tool would trade all of that for template wrangling.
- **A separate interactive API explorer.** The daemon is loopback-only and the console
  already exists as the reference frontend.
- **OpenAPI output.** Interesting only if someone wants Scalar/Redoc; nobody has asked, and
  it is a second rendering of the same schema to keep in sync.

## 5. Open questions

1. ~~Page hierarchy.~~ RESOLVED: pages nest by `.proto` path (see "What actually shipped"),
   which gives every package - service or not - its own canonical page without a separate
   naming scheme, and without the URL-migration cost a BSR-style index -> package ->
   service split would have added on top.
2. **Field-level source links.** Decided against, for now: per-entity links (service,
   method, message, enum) are unambiguous and shipped; a link on every field row was judged
   likely to make the already-dense field tables harder to read, not easier. Revisit by
   looking at a rendered page if a reader asks for it.
3. **Comment rendering (G8).** Still open. Letting comments through as Markdown means
   auditing every existing comment for something that breaks a table cell. Worth it, but it
   is its own unit with its own risk, and it should not ride along inside another phase.
4. **A standalone protoc plugin, emitting semantic HTML.** Explicitly deferred - see
   "Considered and deliberately deferred" above. The research (protogen viability, buf's
   SourceCodeInfo behavior for local plugins, goldmark's raw-HTML/heading-ID interaction)
   is preserved there for whoever picks this up.
