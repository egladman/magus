# Host schemas

The schemas the agent-host integration files are graded against, vendored so no test ever reaches the network. Every row
records where the bytes came from, when, and what they are worth. `hostschema_test.go` at the repository root reads this
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
| `claude-code/hook-output.schema.json` | derived | `https://www.npmjs.com/package/@anthropic-ai/claude-agent-sdk` | 2026-09-10 | `cc6be6de838c16f774c14d3e443b1f7a214f7c183179c70c4d97bc5cb2344e76` | ours |
| `codex/hooks.schema.json` | published | `https://www.schemastore.org/codex-hooks.json` | 2026-09-10 | `3833ef241453facf45f941caff9e246d74eb8c85d7796d2e099dbebb8fe8ed37` | Apache-2.0 (SchemaStore) |
| `codex/pre-tool-use.output.schema.json` | published | `https://raw.githubusercontent.com/openai/codex/main/codex-rs/hooks/schema/generated/pre-tool-use.command.output.schema.json` | 2026-09-10 | `e684f81c63fbb5972892f6a848b49fec68c8ce137931651093d2dd1da56a1dd6` | Apache-2.0 (openai/codex) |
| `cursor/hooks.schema.json` | derived | `https://cursor.com/docs/agent/hooks` | 2026-09-10 | `e7fbd21d80aae97e58fce3921f9bcf9db46eeb97fd9b33a2d4cfc1bb1e48711f` | ours |
| `cursor/hook-output.schema.json` | derived | `https://cursor.com/docs/agent/hooks` | 2026-09-10 | `27c02625f459828a7e28b04fecccfa1d6b007e9bd22c5bdc5814a72513c7dcfc` | ours |

## What each host publishes

Claude Code has a settings schema on SchemaStore that covers the whole `hooks` block, every event name, and the matcher
string, with `additionalProperties: false` at both levels. Its hook STDOUT is TypeScript only, in
`@anthropic-ai/claude-agent-sdk` (0.3.267 when this was read): `SyncHookJSONOutput` and `PreToolUseHookSpecificOutput` in
`sdk.d.ts`. That is why the output row is `derived`.

Codex is the best covered of the four. SchemaStore carries `.codex/hooks.json`, and OpenAI itself generates a schema per
hook event, input and output, under `codex-rs/hooks/schema/generated` in the `openai/codex` repository. The one gap is
that the hooks schema's `hooks` object takes `additionalProperties`, so an event name it does not know still validates.
`hostschema_test.go` closes that itself, by requiring every event name magus ships to be one the schema names.

Cursor publishes a schema for `.cursor/environment.json` (`https://cursor.com/schemas/environment.schema.json`, draft
2019-09) and none for `.cursor/hooks.json`. `https://cursor.com/schemas/hooks.schema.json` answers with the docs SPA,
not with a schema. Both Cursor rows are therefore ours, transcribed from the configuration and output tables on the
hooks page.

OpenCode has no hook config file at all: a plugin intercepts tool calls, so there is nothing here to schema-check. Its
plugin surface is typed instead, and the check already exists elsewhere: `docs/guides/integrations/agents` type-checks
`opencode-plugin.ts` against the `Plugin` type from `@opencode-ai/plugin` (pinned at 1.18.5) on every `magus run lint`.
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
