// Generates JSON Schema from the zod schemas @cursor/sdk publishes, into
// testdata/hosts/cursor/gen/.
//
// WHY THIS EXISTS. Every other Cursor schema in testdata/hosts is `derived`: a person read
// Cursor's minified validator and wrote JSON Schema by hand. A hand transcription is
// partial by construction and silently stale, and it already cost this repository a wrong
// fact recorded as checked (see the CORRECTION in testdata/hosts/SOURCES.md). These
// schemas are not transcribed. They are emitted from the declarations Cursor ships, so the
// only way they go stale is the version pin moving, which the lockfile records and the
// drift gate catches.
//
// WHY zod-to-json-schema RATHER THAN zod's OWN toJSONSchema. Zod 4 ships conversion
// first-party (`z.toJSONSchema`), and that is the path to use for anything on zod 4.
// @cursor/sdk pins `zod: ^3.25.0`, so its schemas are v3 classic objects: zod 4 reads
// `._zod.def` and a v3 schema carries `._def`, so the first-party call throws
// `Cannot read properties of undefined (reading 'def')` on every one of them. Verified,
// not assumed. zod-to-json-schema is the established converter for v3 and is what the
// ecosystem used before v4 absorbed the feature. This import retires itself the day Cursor
// moves to zod 4.
//
// WHAT THIS DOES NOT COVER, so nobody reads more into the output than it says: these are
// the SDK's conversation, message and delta shapes. Cursor exports no schema for the hook
// config or for hook stdout, which are the two contracts magus actually writes and prints.
// Those stay derived from the validator binary until Cursor exports them.

import { mkdir, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import {
  AgentConversationTurnSchema,
  ConversationStepSchema,
  ConversationTurnSchema,
  ImageSchema,
  InteractionUpdateSchema,
  MessageSchema,
  SdkAssistantMessageSchema,
  SdkThinkingMessageSchema,
  SdkUserMessageSchema,
  ShellCommandSchema,
  ShellConversationTurnSchema,
  ShellOutputSchema,
} from "@cursor/sdk";
import { zodToJsonSchema } from "zod-to-json-schema";

// The schemas to emit, by the name they carry in @cursor/sdk's own export list.
//
// NAMED rather than enumerated from the module: a wildcard would silently grow and shrink
// with an upstream release, and the point of a vendored schema is that its contents are a
// decision somebody made. A schema appearing or vanishing upstream should be a diff a
// person reads, which it is only if this list is explicit.
const SCHEMAS = {
  AgentConversationTurn: AgentConversationTurnSchema,
  ConversationStep: ConversationStepSchema,
  ConversationTurn: ConversationTurnSchema,
  Image: ImageSchema,
  InteractionUpdate: InteractionUpdateSchema,
  Message: MessageSchema,
  SdkAssistantMessage: SdkAssistantMessageSchema,
  SdkThinkingMessage: SdkThinkingMessageSchema,
  SdkUserMessage: SdkUserMessageSchema,
  ShellCommand: ShellCommandSchema,
  ShellConversationTurn: ShellConversationTurnSchema,
  ShellOutput: ShellOutputSchema,
} as const;

// draft-07 to match every other schema under testdata/hosts, so one validator reads them
// all. The repo's checker is draft-07 and a mixed directory would need two.
const TARGET = "jsonSchema7";

const HERE = dirname(fileURLToPath(import.meta.url));
// `gen`, not `generated`: this workspace's rule is that generated output lives in a gen/
// directory and carries no suffix, so the DIRECTORY is the signal that nothing here is
// hand-edited.
const OUT = join(HERE, "..", "..", "..", "..", "testdata", "hosts", "cursor", "gen");

async function main(): Promise<void> {
  await mkdir(OUT, { recursive: true });

  const written: string[] = [];
  for (const [name, schema] of Object.entries(SCHEMAS)) {
    const json = zodToJsonSchema(schema, { name, target: TARGET });
    // Stable key order and a trailing newline: this output is committed, so a byte that
    // moves for no reason is a diff somebody has to read and dismiss.
    const body = `${JSON.stringify(json, null, 2)}\n`;
    const file = join(OUT, `${name}.schema.json`);
    await writeFile(file, body, "utf8");
    written.push(name);
  }

  console.log(`cursor-schemas: wrote ${written.length} schema(s) to ${OUT}`);
}

await main();
