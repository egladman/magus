// Manual end-to-end harness for an agent-host descriptor.
//
// It exercises a real locally installed host against an isolated configuration.
// It is deliberately outside ci because a real host can require credentials and
// spend the caller's quota. Opt in and supply collaborator-owned descriptors:
//
//   MAGUS_HOST_E2E=1 magus run host-integration docs/guides/integrations/agents -- \
//     host-e2e/example.json

import { spawnSync, type SpawnSyncReturns } from "node:child_process";
import {
  accessSync,
  constants,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const scenarios = ["command-deny"] as const;
export type Scenario = (typeof scenarios)[number];
type ScenarioDefinition = { name: Scenario; prompt: string };
type Json = null | boolean | number | string | Json[] | { [key: string]: Json };
type Setup =
  | { kind: "json"; path: string; value: Json }
  | { kind: "template"; path: string; source: string };
type Evidence = { kind: "shell-deny" | "guard-deny"; transport: string };

// A descriptor is a dynamic collaborator contract, not a provider registry. The
// runner knows only setup primitives, launch argv/environment and evidence shape.
export type HarnessDescriptor = {
  schemaVersion: 1;
  id: string;
  binary: string;
  guardTemplate?: string;
  variables?: Record<string, string>;
  setup: Setup[];
  launch: { args: string[]; env?: Record<string, string> };
  evidence: Evidence;
};

export type Report = {
  provider: string;
  scenario: Scenario;
  status: "pass" | "skip" | "fail";
  detail: string;
  skipKind?: "unavailable";
  workspace?: string;
};

type Workspace = { root: string; trace: string; response: string };
type Launch = { command: string; args: string[]; env: NodeJS.ProcessEnv };
type TraceRecord = Record<string, unknown>;

const scenarioDefinitions: readonly ScenarioDefinition[] = [
  {
    name: "command-deny",
    prompt: "Use the Bash tool once to run exactly `go test ./...`. Do not make any other change.",
  },
];
const here = path.dirname(fileURLToPath(import.meta.url));
const repository = path.resolve(here, "../../../..");
const templates = path.join(repository, "docs/guides/integrations/agents");

// The probe is disposable-harness-only. It proves that the configured command
// was invoked, then passes the exact hook response back to the host.
export const probeScript = `#!/usr/bin/env sh
sh "$2" > "$MAGUS_HOST_E2E_RESPONSE"
status=$?
printf '{"provider":"%s","scenario":"%s","transport":"shell","exitCode":%s,"responseFile":"hook-response.json"}\\n' \\\
  "$1" "$MAGUS_HOST_E2E_SCENARIO" "$status" >> "$MAGUS_HOST_E2E_TRACE"
cat "$MAGUS_HOST_E2E_RESPONSE"
exit "$status"
`;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function nonEmptyString(value: unknown, name: string): string {
  if (typeof value !== "string" || value.trim() === "")
    throw new Error(`descriptor ${name} must be a non-empty string`);
  return value;
}

function stringArray(value: unknown, name: string): string[] {
  if (!Array.isArray(value) || value.some((item) => typeof item !== "string"))
    throw new Error(`descriptor ${name} must be an array of strings`);
  return [...value] as string[];
}

function stringRecord(value: unknown, name: string): Record<string, string> {
  if (!isRecord(value) || Object.values(value).some((item) => typeof item !== "string"))
    throw new Error(`descriptor ${name} must be an object of strings`);
  return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, String(item)]));
}

function setupEntry(value: unknown, index: number): Setup {
  if (!isRecord(value)) throw new Error(`descriptor setup[${index}] must be an object`);
  const kind = nonEmptyString(value.kind, `setup[${index}].kind`);
  const target = nonEmptyString(value.path, `setup[${index}].path`);
  if (kind === "json") {
    if (!("value" in value)) throw new Error(`descriptor setup[${index}].value is required`);
    return { kind, path: target, value: value.value as Json };
  }
  if (kind === "template")
    return { kind, path: target, source: nonEmptyString(value.source, `setup[${index}].source`) };
  throw new Error(`descriptor setup[${index}].kind ${JSON.stringify(kind)} is unsupported`);
}

export function parseDescriptor(body: string): HarnessDescriptor {
  let parsed: unknown;
  try {
    parsed = JSON.parse(body);
  } catch {
    throw new Error("descriptor is not JSON");
  }
  if (!isRecord(parsed)) throw new Error("descriptor must be a JSON object");
  if (parsed.schemaVersion !== 1) throw new Error("descriptor schemaVersion must be 1");
  if (!Array.isArray(parsed.setup)) throw new Error("descriptor setup must be an array");
  if (!isRecord(parsed.launch)) throw new Error("descriptor launch must be an object");
  if (!isRecord(parsed.evidence)) throw new Error("descriptor evidence must be an object");
  const evidenceKind = nonEmptyString(parsed.evidence.kind, "evidence.kind");
  if (evidenceKind !== "shell-deny" && evidenceKind !== "guard-deny")
    throw new Error(`descriptor evidence.kind ${JSON.stringify(evidenceKind)} is unsupported`);
  return {
    schemaVersion: 1,
    id: nonEmptyString(parsed.id, "id"),
    binary: nonEmptyString(parsed.binary, "binary"),
    ...(parsed.guardTemplate === undefined
      ? {}
      : { guardTemplate: nonEmptyString(parsed.guardTemplate, "guardTemplate") }),
    ...(parsed.variables === undefined
      ? {}
      : { variables: stringRecord(parsed.variables, "variables") }),
    setup: parsed.setup.map(setupEntry),
    launch: {
      args: stringArray(parsed.launch.args, "launch.args"),
      ...(parsed.launch.env === undefined
        ? {}
        : { env: stringRecord(parsed.launch.env, "launch.env") }),
    },
    evidence: {
      kind: evidenceKind,
      transport: nonEmptyString(parsed.evidence.transport, "evidence.transport"),
    },
  };
}

export function selectedDescriptorPaths(args: readonly string[]): string[] {
  const requested = args.filter((arg) => arg !== "--");
  if (requested.length === 0)
    throw new Error("supply one or more harness descriptor paths after --");
  const result: string[] = [];
  for (const requestedPath of requested) {
    const descriptorFile = path.resolve(requestedPath);
    if (!result.includes(descriptorFile)) result.push(descriptorFile);
  }
  return result;
}

function executable(name: string): string | null {
  for (const directory of (process.env.PATH ?? "").split(path.delimiter)) {
    if (directory === "") continue;
    const candidate = path.join(directory, name);
    try {
      accessSync(candidate, constants.X_OK);
      return candidate;
    } catch {
      // An unexecutable file on PATH is not a runnable host.
    }
  }
  return null;
}

export function selectedScenarios(): ScenarioDefinition[] {
  return [...scenarioDefinitions];
}

function shellWord(value: string): string {
  return `'${value.replaceAll("'", "'\\\"'\\\"'")}'`;
}

function guardedCommand(
  descriptor: HarnessDescriptor,
  scenario: Scenario,
  workspace: Workspace,
  guardTemplate: string,
): string {
  return [
    `MAGUS_HOST_E2E_TRACE=${shellWord(workspace.trace)}`,
    `MAGUS_HOST_E2E_RESPONSE=${shellWord(workspace.response)}`,
    `MAGUS_HOST_E2E_SCENARIO=${shellWord(scenario)}`,
    `__MAGUS_AGENT_NAME=${shellWord(descriptor.id)}`,
    `__MAGUS_BIN=${shellWord(path.join(workspace.root, "magus"))}`,
    "sh",
    shellWord(path.join(workspace.root, "host-e2e-probe.sh")),
    shellWord(descriptor.id),
    shellWord(guardTemplate),
  ].join(" ");
}

function interpolate(value: string, variables: Readonly<Record<string, string>>): string {
  return value.replaceAll(/{{([a-z_]+)}}/g, (token, name: string) => {
    const replacement = variables[name];
    if (replacement === undefined)
      throw new Error(`descriptor references unknown variable ${token}`);
    return replacement;
  });
}

function interpolateJson(value: Json, variables: Readonly<Record<string, string>>): Json {
  if (typeof value === "string") return interpolate(value, variables);
  if (Array.isArray(value)) return value.map((item) => interpolateJson(item, variables));
  if (isRecord(value))
    return Object.fromEntries(
      Object.entries(value).map(([key, item]) => [key, interpolateJson(item as Json, variables)]),
    );
  return value;
}

function workspacePath(workspace: Workspace, target: string): string {
  const result = path.resolve(workspace.root, target);
  if (result !== workspace.root && !result.startsWith(`${workspace.root}${path.sep}`))
    throw new Error(
      `descriptor setup path escapes its disposable workspace: ${JSON.stringify(target)}`,
    );
  return result;
}

function descriptorPath(descriptorFile: string, source: string): string {
  return path.resolve(path.dirname(descriptorFile), source);
}

function createWorkspace(): Workspace {
  const root = mkdtempSync(path.join(os.tmpdir(), "magus-host-e2e-"));
  writeFileSync(
    path.join(root, "magusfile.buzz"),
    'import "magus";\nmagus\\project({"no_language": "host e2e fixture"});\n',
  );
  symlinkSync(path.join(repository, "magus"), path.join(root, "magus"));
  writeFileSync(path.join(root, "host-e2e-probe.sh"), probeScript, { mode: 0o755 });
  return {
    root,
    trace: path.join(root, "hook-trace.ndjson"),
    response: path.join(root, "hook-response.json"),
  };
}

function prepareWorkspace(
  descriptor: HarnessDescriptor,
  descriptorFile: string,
  workspace: Workspace,
  scenario: ScenarioDefinition,
): Launch {
  const guardTemplate =
    descriptor.guardTemplate === undefined
      ? ""
      : descriptorPath(descriptorFile, descriptor.guardTemplate);
  const variables: Record<string, string> = {
    workspace: workspace.root,
    repository,
    templates,
    descriptor_dir: path.dirname(descriptorFile),
    trace: workspace.trace,
    response: workspace.response,
    scenario: scenario.name,
    provider: descriptor.id,
  };
  for (const [name, value] of Object.entries(descriptor.variables ?? {})) {
    if (name in variables)
      throw new Error(`descriptor variable ${JSON.stringify(name)} shadows a reserved variable`);
    variables[name] = interpolate(value, variables);
  }
  variables.guard_command = guardedCommand(descriptor, scenario.name, workspace, guardTemplate);
  for (const setup of descriptor.setup) {
    const target = workspacePath(workspace, interpolate(setup.path, variables));
    mkdirSync(path.dirname(target), { recursive: true });
    if (setup.kind === "json") {
      writeFileSync(
        target,
        `${JSON.stringify(interpolateJson(setup.value, variables), null, 2)}\n`,
      );
      continue;
    }
    writeFileSync(
      target,
      interpolate(readFileSync(descriptorPath(descriptorFile, setup.source), "utf8"), variables),
    );
  }
  return {
    command: executable(descriptor.binary) ?? descriptor.binary,
    args: descriptor.launch.args.map((arg) => interpolate(arg, variables)),
    env: {
      ...process.env,
      ...Object.fromEntries(
        Object.entries(descriptor.launch.env ?? {}).map(([key, value]) => [
          key,
          interpolate(value, variables),
        ]),
      ),
    },
  };
}

function evidenceRecords(body: string): TraceRecord[] {
  const lines = body.split("\n").filter(Boolean);
  if (lines.length === 0) throw new Error("the host wrote an empty hook evidence trace");
  return lines.map((line, index) => {
    let parsed: unknown;
    try {
      parsed = JSON.parse(line);
    } catch {
      throw new Error(`hook evidence line ${index + 1} is not JSON`);
    }
    if (!isRecord(parsed)) throw new Error(`hook evidence line ${index + 1} is not an object`);
    return parsed;
  });
}

function stringField(record: TraceRecord, field: string): string {
  const value = record[field];
  if (typeof value !== "string") throw new Error(`hook evidence has no string ${field}`);
  return value;
}

function denyReason(response: string): string {
  let parsed: unknown;
  try {
    parsed = JSON.parse(response);
  } catch {
    throw new Error("hook response is not JSON");
  }
  if (!isRecord(parsed)) throw new Error("hook response is not an object");
  const output = parsed.hookSpecificOutput;
  if (!isRecord(output)) throw new Error("hook response has no hookSpecificOutput object");
  if (output.hookEventName !== "PreToolUse") throw new Error("hook response is not for PreToolUse");
  if (output.permissionDecision !== "deny")
    throw new Error("hook response does not deny the command");
  const reason = output.permissionDecisionReason;
  if (typeof reason !== "string" || reason.trim() === "")
    throw new Error("hook response has no denial reason");
  return reason;
}

export function validateEvidence(
  descriptor: HarnessDescriptor,
  scenario: Scenario,
  trace: string,
  response?: string,
): string {
  const records = evidenceRecords(trace);
  if (records.length !== 1) throw new Error(`expected one hook record, got ${records.length}`);
  const record = records[0];
  if (stringField(record, "provider") !== descriptor.id)
    throw new Error(`hook evidence did not identify ${descriptor.id}`);
  if (stringField(record, "scenario") !== scenario)
    throw new Error(`hook evidence did not identify ${scenario}`);
  if (stringField(record, "transport") !== descriptor.evidence.transport)
    throw new Error(`hook evidence is not from ${descriptor.evidence.transport}`);
  if (descriptor.evidence.kind === "shell-deny") {
    if (record.exitCode !== 0) throw new Error(`hook command exited ${String(record.exitCode)}`);
    if (stringField(record, "responseFile") !== "hook-response.json")
      throw new Error("hook evidence did not preserve the raw response file");
    if (response === undefined) throw new Error("the hook did not persist its raw response");
    return denyReason(response);
  }
  if (stringField(record, "outcome") !== "deny") {
    const reason = stringField(record, "reason");
    throw new Error(`production hook did not deny: ${reason}`);
  }
  const reason = stringField(record, "reason");
  if (!reason.startsWith("[magus guard] ") || reason.trim() === "[magus guard]")
    throw new Error("deny evidence is not a production guard denial");
  return reason;
}

function verifyScenarioEvidence(
  descriptor: HarnessDescriptor,
  scenario: Scenario,
  workspace: Workspace,
): string {
  if (!existsSync(workspace.trace))
    throw new Error("the host completed without invoking its configured hook");
  return validateEvidence(
    descriptor,
    scenario,
    readFileSync(workspace.trace, "utf8"),
    existsSync(workspace.response) ? readFileSync(workspace.response, "utf8") : undefined,
  );
}

const maxHostLogBytes = 64 * 1024;

function cappedHostLog(output: string | Buffer | null | undefined): string {
  const body = output === undefined || output === null ? "" : String(output);
  if (Buffer.byteLength(body) <= maxHostLogBytes) return body;
  return `${body.slice(0, maxHostLogBytes)}\n[truncated after ${maxHostLogBytes} bytes]\n`;
}

function retainFailureLogs(workspace: Workspace, result: SpawnSyncReturns<string>): string {
  writeFileSync(path.join(workspace.root, "host.stdout.log"), cappedHostLog(result.stdout));
  writeFileSync(path.join(workspace.root, "host.stderr.log"), cappedHostLog(result.stderr));
  if (result.error)
    writeFileSync(path.join(workspace.root, "host.error.log"), `${result.error.message}\n`);
  return "logs=host.stdout.log,host.stderr.log";
}

function finishSuccess(workspace: Workspace): Pick<Report, "detail" | "workspace"> {
  if (process.env.MAGUS_HOST_E2E_KEEP === "1")
    return {
      detail: `host dispatched the configured hook; retained workspace=${workspace.root}`,
      workspace: workspace.root,
    };
  rmSync(workspace.root, { recursive: true, force: true });
  return { detail: "host dispatched the configured hook" };
}

function runScenario(
  descriptor: HarnessDescriptor,
  descriptorFile: string,
  scenario: ScenarioDefinition,
): Report {
  if (executable(descriptor.binary) === null)
    return {
      provider: descriptor.id,
      scenario: scenario.name,
      status: "skip",
      skipKind: "unavailable",
      detail: `${descriptor.binary} is not on PATH`,
    };
  const workspace = createWorkspace();
  const command = prepareWorkspace(descriptor, descriptorFile, workspace, scenario);
  const result = spawnSync(command.command, command.args, {
    cwd: workspace.root,
    env: command.env,
    encoding: "utf8",
    timeout: 120_000,
  });
  if (result.error) {
    const logs = retainFailureLogs(workspace, result);
    return {
      provider: descriptor.id,
      scenario: scenario.name,
      status: "fail",
      detail: `${result.error.message}; ${logs}; workspace=${workspace.root}`,
      workspace: workspace.root,
    };
  }
  try {
    verifyScenarioEvidence(descriptor, scenario.name, workspace);
  } catch (error) {
    const detail = error instanceof Error ? error.message : String(error);
    const logs = retainFailureLogs(workspace, result);
    return {
      provider: descriptor.id,
      scenario: scenario.name,
      status: "fail",
      detail: `${detail}; exit=${result.status}; ${logs}; workspace=${workspace.root}`,
      workspace: workspace.root,
    };
  }
  if (result.status !== 0) {
    const logs = retainFailureLogs(workspace, result);
    return {
      provider: descriptor.id,
      scenario: scenario.name,
      status: "fail",
      detail: `host exited ${result.status} after dispatching the hook; ${logs}; workspace=${workspace.root}`,
      workspace: workspace.root,
    };
  }
  return {
    provider: descriptor.id,
    scenario: scenario.name,
    status: "pass",
    ...finishSuccess(workspace),
  };
}

export function resultCode(reports: readonly Pick<Report, "status">[]): number {
  if (reports.some((report) => report.status === "fail")) return 1;
  if (!reports.some((report) => report.status === "pass")) return 2;
  return 0;
}

export function main(args: readonly string[]): number {
  if (process.env.MAGUS_HOST_E2E !== "1") {
    console.error(
      "refusing to start real agent hosts; rerun with MAGUS_HOST_E2E=1 after confirming local credentials and quota",
    );
    return 2;
  }
  let descriptorFiles: string[];
  try {
    descriptorFiles = selectedDescriptorPaths(args);
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    return 2;
  }
  const reports: Report[] = [];
  for (const descriptorFile of descriptorFiles) {
    let descriptor: HarnessDescriptor;
    try {
      descriptor = parseDescriptor(readFileSync(descriptorFile, "utf8"));
    } catch (error) {
      reports.push({
        provider: descriptorFile,
        scenario: "command-deny",
        status: "fail",
        detail: error instanceof Error ? error.message : String(error),
      });
      continue;
    }
    reports.push(
      ...selectedScenarios().map((scenario) => runScenario(descriptor, descriptorFile, scenario)),
    );
  }
  for (const report of reports) console.log(JSON.stringify(report));
  const code = resultCode(reports);
  if (code === 2)
    console.error(
      "inconclusive: every selected host was unavailable; install a host or select a different descriptor",
    );
  return code;
}

if (process.argv[1] === fileURLToPath(import.meta.url))
  process.exitCode = main(process.argv.slice(2));
