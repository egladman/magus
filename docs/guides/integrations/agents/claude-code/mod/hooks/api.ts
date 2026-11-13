import type { ActivityLine, AgentRow, JobBlock, JobRow, JobState, Listing, Role, RunRow } from '../types'
import { MagusError, fail, isRecord, ok, parseJSON, type Result } from './result'

export type { ActivityLine, AgentRow, JobBlock, JobRow, Listing, Role, RunRow }

// The Connect procedures the pane calls, the same ones the console's PWA calls.
export const LIST_JOBS = '/magus.job.v1alpha1.JobService/ListJobs'
export const LIST_INVOCATIONS = '/magus.viewer.v1alpha1.ViewerService/ListInvocations'
export const LIST_ACTIVITY = '/magus.activity.v1alpha1.ActivityService/ListActivityEvents'

// The server's socket from `magus server status -o json`: pool.socket with its unix://
// scheme dropped, or null when no server answered.
export function socketFromStatus(stdout: string): string | null {
  const s = parseJSON(stdout)
  if (s.error || !isRecord(s.value) || s.value.server == null) return null
  const pool = s.value.pool
  const socket = isRecord(pool) ? str(pool.socket) : ''
  return socket ? socket.replace(/^unix:\/\//, '') : null
}

// A Connect error body ({ code, message }) as one line; the raw text when it is not one.
export function connectError(status: number, text: string): string {
  const e = parseJSON(text)
  if (!e.error && isRecord(e.value) && (e.value.code || e.value.message)) {
    return `${str(e.value.code) || status}: ${str(e.value.message)}`.trim()
  }
  return `HTTP ${status}${text ? `: ${text.slice(0, 200)}` : ''}`
}

// protojson writes int64 as a string and leaves zero values out.
const int = (v: unknown): number => (typeof v === 'number' ? v : typeof v === 'string' ? Number(v) || 0 : 0)
const time = (v: unknown): number => (typeof v === 'string' ? Date.parse(v) || 0 : 0)
const str = (v: unknown): string => (v === undefined || v === null ? '' : String(v))
const strs = (v: unknown): string[] => (Array.isArray(v) ? v.map(String) : [])
const records = (v: unknown): Record<string, unknown>[] => (Array.isArray(v) ? v.filter(isRecord) : [])
const record = (v: unknown): Record<string, unknown> => (isRecord(v) ? v : {})
const enumWord = (v: unknown, prefix: string): string =>
  typeof v === 'string' ? v.replace(prefix, '').toLowerCase() : ''

const JOB_STATES: readonly JobState[] = ['declared', 'running', 'exited', 'pass', 'fail', 'no_return']

// jobState reads a state the server wrote; an empty one is a job nobody has taken yet.
function jobState(v: unknown): Result<JobState> {
  const word = str(v) || 'declared'
  const state = JOB_STATES.find(s => s === word)
  return state
    ? ok(state)
    : fail(new MagusError({ code: '', message: `ListJobs answered an unknown job state ${word}` }))
}

// answer parses one Connect response body, which is a JSON object for every procedure here.
function answer(text: string, procedure: string): Result<Record<string, unknown>> {
  const doc = parseJSON(text)
  if (doc.error)
    return fail(
      new MagusError({ code: '', message: `${procedure} answered a body that is not JSON: ${doc.error.message}` }),
    )
  return isRecord(doc.value)
    ? ok(doc.value)
    : fail(new MagusError({ code: '', message: `${procedure} answered JSON that is not an object` }))
}

export function parseJobs(text: string, fetchedAt: number): Result<Listing> {
  const doc = answer(text, 'ListJobs')
  if (doc.error) return doc
  const rows: JobRow[] = []
  for (const j of records(doc.value.jobs)) {
    const agent = record(j.agent)
    const model = record(agent.model)
    const state = jobState(j.state)
    if (state.error) return state
    const holder = enumWord(j.holder, 'JOB_HOLDER_')
    rows.push({
      id: str(j.id),
      parent: str(j.parent),
      state: state.value,
      model: str(model.tier) || str(model.id),
      holder: holder === 'session' ? '' : holder,
      criteria: str(agent.instructions) || str(j.description),
      check: str(j.check),
      writePaths: strs(record(j.paths).write),
      goals: records(j.goals).map(g => `${str(g.kind) || '?'} ${str(g.id)}`.trim()),
      proof: str(j.writeProof),
      endReason: str(j.endReason),
      baseVerdict: str(j.baseVerdict),
      updated: int(j.updated),
    })
  }
  const blocked: JobBlock[] = records(doc.value.blocked).map(b => ({
    job: str(b.job),
    on: str(b.on),
    state: str(b.state),
  }))
  return ok({
    rows,
    overdue: strs(doc.value.overdue),
    orphans: strs(doc.value.orphans),
    stale: strs(doc.value.stale),
    blocked,
    fetchedAt,
  })
}

export function parseRuns(text: string): Result<RunRow[]> {
  const doc = answer(text, 'ListInvocations')
  if (doc.error) return doc
  return ok(
    records(doc.value.invocations).map(i => ({
      id: str(i.id),
      command: strs(isRecord(i.command) ? i.command.arguments : undefined).join(' '),
      startedAt: time(i.startTime),
      endedAt: time(i.endTime),
      status: enumWord(i.status, 'STATUS_'),
    })),
  )
}

export function parseActivity(text: string): Result<ActivityLine[]> {
  const doc = answer(text, 'ListActivityEvents')
  if (doc.error) return doc
  return ok(
    records(doc.value.events).map(e => ({
      at: time(e.time),
      kind: enumWord(e.kind, 'KIND_'),
      action: str(e.action),
      outcome: enumWord(e.outcome, 'OUTCOME_'),
      actor: str(e.actor),
      preview: str(e.preview),
    })),
  )
}

// The jobs a spawn description `<parent>/<role> <job>` can name, in the order the guard
// resolves them: `<job>`, else `<parent>/<job>`. Empty for any other description.
export function candidateJobs(description: string): string[] {
  const [lead, job, ...rest] = description.trim().split(/\s+/)
  if (lead === undefined || job === undefined || rest.length > 0 || !lead.includes('/')) return []
  const parent = lead.slice(0, lead.lastIndexOf('/'))
  return [job, `${parent}/${job}`]
}

const ROLES: readonly Role[] = ['orchestrator', 'integrator', 'worker']

// The role a description `<parent>/<role> <job>` names; null for any other.
export function roleOf(description: string): Role | null {
  if (candidateJobs(description).length === 0) return null
  const lead = description.trim().split(/\s+/)[0] ?? ''
  const role = lead.slice(lead.lastIndexOf('/') + 1)
  return ROLES.find(r => r === role) ?? null
}
