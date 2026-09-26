# Host schemas

The schemas the agent-host integration files are graded against, vendored so no test ever reaches the network. Every row
records where the bytes came from, when, and what they are worth. `conventions_test.go` at the repository root reads this
table: a vendored file whose digest stops matching the `sha256` column fails the build, so a hand-edit cannot pass for a
refresh.

Two origins, and the difference is the whole point of the table:

- `generated` is EMITTED from a declaration the host publishes, by a target in this repository. Nobody
  transcribed it, so it cannot be partial or stale the way a reading can: its source is a pinned package and the drift
  gate compares the bytes. Prefer this over `derived` wherever a host exports anything machine-readable.
- `published` is the host's own artifact, fetched verbatim. A failure against one of these is ours.
- `derived` is OURS, transcribed by hand because that host publishes no schema for that surface. A failure against one of
  these means either magus drifted or our transcription is stale, and the `url` is where to settle it. Do not present
  these as the host's contract.

## Table

| file | origin | url | read | sha256 | license |
| --- | --- | --- | --- | --- | --- |
| `claude-code/settings.schema.json` | published | `https://www.schemastore.org/claude-code-settings.json` | 2026-09-10 | `6d4a6e3c7adedffce8079ccaef0a4bab5f5718b054421b4475c788a0ae4bedfe` | Apache-2.0 (SchemaStore) |
| `claude-code/hook-output.schema.json` | derived | `https://www.npmjs.com/package/@anthropic-ai/claude-agent-sdk` | 2026-09-10 | `7b928f72b0f43ba6ac978512791df374cb5f2d7fa9cd69df313c1357fc8e9b9d` | ours |
| `codex/hooks.schema.json` | published | `https://www.schemastore.org/codex-hooks.json` | 2026-09-10 | `3833ef241453facf45f941caff9e246d74eb8c85d7796d2e099dbebb8fe8ed37` | Apache-2.0 (SchemaStore) |
| `codex/pre-tool-use.command.output.schema.json` | published | `https://raw.githubusercontent.com/openai/codex/main/codex-rs/hooks/schema/generated/pre-tool-use.command.output.schema.json` | 2026-09-10 | `e684f81c63fbb5972892f6a848b49fec68c8ce137931651093d2dd1da56a1dd6` | Apache-2.0 (openai/codex) |
| `codex/pre-tool-use.command.input.schema.json` | published | `https://raw.githubusercontent.com/openai/codex/main/codex-rs/hooks/schema/generated/pre-tool-use.command.input.schema.json` | 2026-09-16 | `fabed428f0fe75767c5700208b166da5faef4e031d601dfc8bff2f96d340c682` | Apache-2.0 (openai/codex) |
| `codex/permission-request.command.output.schema.json` | published | `https://raw.githubusercontent.com/openai/codex/7abf2a3b5cbe08ca875d677dcd027528f9556152/codex-rs/hooks/schema/generated/permission-request.command.output.schema.json` | 2026-09-17 | `749c73245b4b6d43537c3049f76720ab1c2bd48d7e4752b744b376925b9d57a1` | Apache-2.0 (openai/codex) |
| `codex/session-start.command.input.schema.json` | published | `https://raw.githubusercontent.com/openai/codex/main/codex-rs/hooks/schema/generated/session-start.command.input.schema.json` | 2026-09-16 | `54168cf0bb3641bbc55dcdc58aa3803651d4f24499339061f3e9bb0ef9095633` | Apache-2.0 (openai/codex) |
| `codex/session-start.command.output.schema.json` | published | `https://raw.githubusercontent.com/openai/codex/main/codex-rs/hooks/schema/generated/session-start.command.output.schema.json` | 2026-09-16 | `f375e6de1c59ecbabd8c1aff05a67976d0f3aa2ef061808838de4c7c20be1c71` | Apache-2.0 (openai/codex) |
| `codex/stop.command.input.schema.json` | published | `https://raw.githubusercontent.com/openai/codex/main/codex-rs/hooks/schema/generated/stop.command.input.schema.json` | 2026-09-16 | `7db4793c404b5c46b230c27b9507eb1a558fd958689d8715221c5dd81351a06a` | Apache-2.0 (openai/codex) |
| `codex/stop.command.output.schema.json` | published | `https://raw.githubusercontent.com/openai/codex/main/codex-rs/hooks/schema/generated/stop.command.output.schema.json` | 2026-09-16 | `dc2b30e84c97beca5825aa64ca46e1337e402781dc5a9142b67111d10523f15c` | Apache-2.0 (openai/codex) |
| `cursor/hooks.schema.json` | derived-from-binary | `https://downloads.cursor.com/lab/2026.09.08-6caf4ff/darwin/arm64/agent-cli-package.tar.gz` | 2026-09-10 | `9df2d5591a4a0dd83f030037f313ea40b6281c7ae217ec16f6fec623d1333333` | ours |
| `cursor/hook-output.schema.json` | derived-from-binary | `https://downloads.cursor.com/lab/2026.09.08-6caf4ff/darwin/arm64/agent-cli-package.tar.gz` | 2026-09-10 | `3fa77adf5158ad5551a1cd1763e8bb44db4c059b26721fbbed5f2ed036c98671` | ours |
| `cursor/post-tool-use.output.schema.json` | derived-from-binary | `https://downloads.cursor.com/lab/2026.09.08-6caf4ff/darwin/arm64/agent-cli-package.tar.gz` | 2026-09-10 | `7696693a85b717d9f51db735b122203aa8071514784d50e17cf7cb640632f2af` | ours |

## What each host publishes

Claude Code has a settings schema on SchemaStore that covers the whole `hooks` block, every event name, and the matcher
string, with `additionalProperties: false` at both levels. Its hook STDOUT is TypeScript only, in
`@anthropic-ai/claude-agent-sdk` (0.3.267 when this was read): `SyncHookJSONOutput` and `PreToolUseHookSpecificOutput` in
`sdk.d.ts`. That is why the output row is `derived`.

Codex is the best covered of the four. SchemaStore carries `.codex/hooks.json`, and OpenAI itself generates a schema per
hook event, input and output, under `codex-rs/hooks/schema/generated` in the `openai/codex` repository. The one gap is
that the hooks schema's `hooks` object takes `additionalProperties`, so an event name it does not know still validates.
`internal/agent/harness_test.go` closes that itself, by requiring every event name magus ships to be one the schema names.

Cursor publishes a schema for `.cursor/environment.json` (`https://cursor.com/schemas/environment.schema.json`, draft
2019-09) and none for `.cursor/hooks.json`. `https://cursor.com/schemas/hooks.schema.json` answers with the docs SPA,
not with a schema. What Cursor does ship is the VALIDATOR: the bundled hooks package rejects an unknown event name, a
missing or fractional `version`, and a malformed handler before any hook fires, and validates each event's stdout with
its own function. The three Cursor rows are transcribed from that code, read 2026-09-10 out of two artifacts that carry
the same bundle: the cursor-agent CLI package (2026.09.08-6caf4ff, `dist-package/index.js`, module
`../hooks/dist/index.js`; sha256 `9c456cc432adc476202a2b09c21a10944fdf484b3ca55dcbf939bbc8ce30dcbe`) and the desktop app
(3.19.19, `resources/app/extensions/cursor-agent-host/dist/main.js`, from
`https://downloads.cursor.com/production/6496ea8a068aebfcd21990e70ff522e9abf10c8c/linux/x64/deb/amd64/deb/cursor_3.19.19_amd64.deb`,
sha256 `2e633ff3e6871aa7bdf7879f91693e2163a11bff5a0e085426b1139fabb3dd62`). `derived-from-binary` names that origin: the
URL is an artifact to unpack and re-read, not bytes to diff, so the refresh script skips these rows and a Cursor release
is re-read by hand. Where the docs page and the validator disagree (the page types `matcher` as an object; the validator
requires a string that compiles as a regex), the schema follows the validator, because the binary is what runs.

The same bundle reaches npm, which is better provenance than either artifact above and is what the upstream table below
pins. `@cursor/sdk` carries the hooks module: `dist/esm/357.js` holds the event-name map and the stdout validator, and
the registry serves an immutable tarball per version with an `integrity` the fetch verifies, where a
`downloads.cursor.com/lab/...` URL is a moving build that will eventually 404. It is still `derived`: the validator is
zod, but it is INTERNAL, so none of this is generatable through a supported interface. `@cursor/sdk`'s `exports` map
offers `.`, `./agent` and `./sqlite` only, the validator is a mangled symbol, and `dist/esm/357.js` is a chunk name that
moves whenever the bundler reorders. What the upstream table buys is a tripwire, not a generator: when the recorded
digest stops matching, a human re-reads the bundle.

CORRECTION, 2026-09-16. This paragraph previously recorded that `@cursor/sdk` exports no hook types. It was WRONG, and
wrong in the way that does the most damage: it told the next reader not to look.

`@cursor/sdk` 1.0.31 declares `types: ./dist/esm/index.d.ts` and ships 120 exported zod schemas under
`dist/esm/vendor/cursor-sdk-shared/`, including `ShellArgsSchema`, `ShellToolCallSchema`, `EditArgsSchema` and
`EditToolCallSchema` -- the payload shapes behind the two events magus wires here. The search that produced the old claim
looked for the word `hook`. Cursor's vocabulary is `*Schema`, so it found nothing and the nothing got written down as a
fact.

READ THIS BEFORE RECORDING ANOTHER ABSENCE. An absence is only worth recording with the search that produced it, and a
search by one vendor's word for a thing is not a search. Grep the package for the EVENT NAMES and for the shapes
(`z.object`, `export declare const .*Schema`, `export declare type .*Input`), and list the declaration files before
concluding a package carries nothing. The same mistake had already been made once on this package and corrected once;
this is its second recurrence, which is why the instruction is here and not in a commit message.

What is still open, and must not be written down as settled until somebody checks: whether the hook ENVELOPE (the stdout
contract carrying `permission`, `user_message`, `agentInitiated`) is among those 120, or whether only the tool-call
payloads are and the envelope stays internal to the bundle. The first case makes Cursor fully derivable; the second
makes the payloads derivable and leaves the envelope a tripwire.

### Codex publishes 23 of these; we take 6

`codex-rs/hooks/schema/generated/` in openai/codex holds a generated input AND output schema for each of its 12 hook
events. The six rows above are the three events magus wires -- `PreToolUse`, `SessionStart`, `Stop` -- in both
directions. The other sixteen are not taken, deliberately: they describe events magus does not wire, so nothing here
would grade anything against them, and a vendored schema no test reads is a file that rots.

Recorded rather than left to be rediscovered, because that is the failure this file already suffered once: the
directory is real, first-party, machine-generated, and complete, so wiring a new Codex event starts by taking its two
rows from there, never by reading prose. It also answers capability questions outright. `pre-compact.command.output` is
`additionalProperties: false` over `continue`, `stopReason`, `suppressOutput` and `systemMessage` with no
`hookSpecificOutput`, which is why magus does not try to steer compaction on Codex -- proven from OpenAI's own artifact
rather than inferred from a docs page.

The `*.input` rows are new in kind. Every other schema here grades what magus WRITES; these grade what a host SENDS it,
which nothing checked on any host before.

### Generated from @cursor/sdk

`testdata/hosts/cursor/gen/` holds twelve schemas emitted from the zod `@cursor/sdk` exports at its package root,
by `magus run cursor-schemas-generate docs/guides/integrations/agents`. They are the SDK's conversation, message and
delta shapes, and they are NOT the hook config or the hook stdout envelope -- Cursor exports no schema for either, so
those two stay `derived` from the validator binary above. Read the generated files for what the SDK sends, never for
what a hook receives.

The converter is `zod-to-json-schema`, and that choice is against the grain, so here is why. Zod 4 ships conversion
first-party as `z.toJSONSchema`, which is the path to use for anything on zod 4 and what any new code here should reach
for. It does not work on these: `@cursor/sdk` pins `zod: ^3.25.0`, zod 4 reads `._zod.def` while a v3 schema carries
`._def`, and the first-party call throws `Cannot read properties of undefined (reading 'def')` on every one of them.
Verified against the real package rather than reasoned about. `zod-to-json-schema` is the established converter for v3.
The dependency retires itself the day Cursor moves to zod 4, and the call becomes `z.toJSONSchema`.

The emitted list in `cursor-schemas.ts` is explicit rather than a wildcard over the module, so a schema appearing or
vanishing upstream is a diff somebody reads instead of a silent change in what this repository claims to know.

### Cursor's hook events

The validator rejects an event name it does not know, so this list IS the contract, and
`cursor/hooks.schema.json` must name all of it. Read from the `dist/esm/357.js` event map at the version pinned below:

`beforeShellExecution`, `beforeMCPExecution`, `afterShellExecution`, `afterMCPExecution`, `beforeReadFile`,
`afterFileEdit`, `beforeTabFileRead`, `afterTabFileEdit`, `stop`, `beforeSubmitPrompt`, `afterAgentResponse`,
`afterAgentThought`, `sessionStart`, `sessionEnd`, `preCompact`, `subagentStart`, `subagentStop`, `preToolUse`,
`postToolUse`, `postToolUseFailure`, `workspaceOpen`.

Twenty-one, and magus wires five. `preCompact` is the notable absence: magus has a post-compaction brief on the hosts
that offer the moment, and Cursor offers it.

## Upstream artifacts

What the `derived` rows above were transcribed FROM. The rows in the first table digest OUR files; these digest the
declarations they came out of, which is the half that was missing: nothing noticed when an upstream release changed the
validator, because only our own transcription was ever hashed.

A SLICE, not the file. `marker` is located in the named file and a fixed window from it is what gets digested, because a
whole-file digest reports every release as drift whatever moved in it. Measured: `@anthropic-ai/claude-agent-sdk` went
0.3.267 to 0.3.273 and `sdk.d.ts` changed, while `SyncHookJSONOutput` did not move a byte. A tripwire that fires on
every SDK bump is one somebody mutes, and a muted tripwire is worse than none.

A marker the tool cannot find is an ERROR, never a pass. That is the failure the slice buys its quiet with: if the
declaration is renamed or the bundler reorders a chunk, the window silently becomes something else, so not finding it
has to be loud.

The window is 2048 BYTES, not characters. Buzz indexes strings by byte, so a digest computed with character slicing
disagrees with the one the tool computes as soon as the window covers anything outside ASCII, and `sdk.d.ts` does.

It was 512, and 512 was too small to do the job. `SyncHookJSONOutput`'s scalar fields end near marker+490 while its
`hookSpecificOutput` union runs to marker+1187, so the window stopped at that field's own colon and covered
none of the twenty arms. A new arm on that union is precisely the upstream change worth catching, and the tripwire
reported `same` for it. Both digests above were re-recorded when the window widened, which is the documented cost of
changing it. A window that ends mid-declaration is worse than no window: it reports success for the change it exists to
see.

| package | version | file | marker | sha256 |
| --- | --- | --- | --- | --- |
| `@anthropic-ai/claude-agent-sdk` | 0.3.273 | `sdk.d.ts` | `export declare type SyncHookJSONOutput` | `7a5eae6222c0508b9d2793b7a049f535087b0de702aafddd68fe16c69d5de052` |
| `@cursor/sdk` | 1.0.31 | `dist/esm/357.js` | `beforeShellExecution:"beforeShellExecution"` | `5f626fef84ff0feba2b294dcb1029a59152ff743fbaffbfc9826defc8307544d` |

OpenCode has no hook config file at all: a plugin intercepts tool calls, so there is nothing here to schema-check. Its
plugin surface is typed instead, and the check already exists elsewhere: `docs/guides/integrations/agents` type-checks
`opencode-plugin.ts` against the `Plugin` type from `@opencode-ai/plugin` (pinned at 1.18.30, the current release when
this was read) on every `magus run lint`.
`https://opencode.ai/config.json` is a real draft 2020-12 schema for `opencode.json`, and magus ships no such file, so
it is deliberately not vendored.

## Refreshing

`tools/host-schemas.buzz` re-fetches the `published` rows and reports what moved. It never runs from a test.

```sh
HOST_SCHEMAS_MODE=verify magus buzz tools/host-schemas.buzz   # re-fetch, report drift, change nothing
HOST_SCHEMAS_MODE=fetch  magus buzz tools/host-schemas.buzz   # re-fetch and write, then update sha256 above
```

A `derived` row has no fetchable artifact, so `verify` skips it and prints the URL to re-read by hand. Refresh those
against the type or the table they were transcribed from, then update the `read` date and the digest in the same commit.
