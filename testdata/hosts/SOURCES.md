# Host schemas

The schemas the agent-host integration files are graded against, vendored so no test ever reaches the network. Every row
records where the bytes came from, when, and what they are worth. `host_schema_test.go` at the repository root reads this
table: a vendored file whose digest stops matching the `sha256` column fails the build, so a hand-edit cannot pass for a
refresh.

Two origins, and the difference is the whole point of the table:

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
`host_schema_test.go` closes that itself, by requiring every event name magus ships to be one the schema names.

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

`@cursor/sdk` was checked for exported hook TYPES on 2026-09-16 and has none, so it cannot serve the role
`@anthropic-ai/claude-agent-sdk` serves for Claude Code. Recorded because the absence is the kind of thing that invites
the same search twice: the package is a TypeScript SDK, it obviously ought to carry them, and it does not.

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

The window is 512 BYTES, not characters. Buzz indexes strings by byte, so a digest computed with character slicing
disagrees with the one the tool computes as soon as the window covers anything outside ASCII, and `sdk.d.ts` does.

| package | version | file | marker | sha256 |
| --- | --- | --- | --- | --- |
| `@anthropic-ai/claude-agent-sdk` | 0.3.273 | `sdk.d.ts` | `export declare type SyncHookJSONOutput` | `184e4135dc77d5fc8c05683856b4c5ea0a83258a85ed37e5cc0ec965e3535ff8` |
| `@cursor/sdk` | 1.0.31 | `dist/esm/357.js` | `beforeShellExecution:"beforeShellExecution"` | `4f6248c31d295f9fd2ee9ecaaea4898002b3790954761de161f900a5d8350860` |

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
