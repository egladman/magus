import type { ActivityLine, AgentRow, JobBlock, JobRow, JobState, Listing, RunRow } from '../types'

export type { ActivityLine, AgentRow, JobBlock, JobRow, Listing, RunRow }

// The Connect procedures the pane calls, the same ones the console's PWA calls.
export const LIST_JOBS = '/magus.job.v1alpha1.JobService/ListJobs'
export const LIST_INVOCATIONS = '/magus.viewer.v1alpha1.ViewerService/ListInvocations'
export const LIST_ACTIVITY = '/magus.activity.v1alpha1.ActivityService/ListActivityEvents'

// The server's socket from `magus server status -o json`: pool.socket with its unix://
// scheme dropped, or null when no server answered.
export function socketFromStatus(stdout: string): string | null {
  try {
    const s = JSON.parse(stdout) as { server?: unknown; pool?: { socket?: string } }
    const socket = s.server != null ? (s.pool?.socket ?? '') : ''
    return socket ? socket.replace(/^unix:\/\//, '') : null
  } catch {
    return null
  }
}

// A Connect error body ({ code, message }) as one line; the raw text when it is not one.
export function connectError(status: number, text: string): string {
  try {
    const e = JSON.parse(text) as { code?: string; message?: string }
    if (e.code || e.message) return `${e.code ?? status}: ${e.message ?? ''}`.trim()
  } catch {
    // not JSON
  }
  return `HTTP ${status}${text ? `: ${text.slice(0, 200)}` : ''}`
}

// protojson writes int64 as a string and leaves zero values out.
const int = (v: unknown): number => (typeof v === 'number' ? v : typeof v === 'string' ? Number(v) || 0 : 0)
const time = (v: unknown): number => (typeof v === 'string' ? Date.parse(v) || 0 : 0)
const strs = (v: unknown): string[] => (Array.isArray(v) ? v.map(String) : [])
const enumWord = (v: unknown, prefix: string): string =>
  typeof v === 'string' ? v.replace(prefix, '').toLowerCase() : ''

type RawJob = Record<string, unknown> & { goals?: { id?: string; kind?: string }[] }

export function parseJobs(text: string, fetchedAt: number): Listing {
  const doc = JSON.parse(text) as {
    jobs?: RawJob[]
    overdue?: string[]
    orphans?: string[]
    stale?: string[]
    blocked?: { job?: string; on?: string; state?: string }[]
  }
  const rows: JobRow[] = (doc.jobs ?? []).map(j => {
    const holder = enumWord(j.holder, 'JOB_HOLDER_')
    return {
      id: String(j.id ?? ''),
      parent: String(j.parent ?? ''),
      state: (String(j.state ?? '') || 'declared') as JobState,
      model: String(j.model ?? ''),
      holder: holder === 'session' ? '' : holder,
      criteria: String(j.criteria ?? j.description ?? ''),
      check: String(j.check ?? ''),
      writePaths: strs(j.writePaths),
      goals: (j.goals ?? []).map(g => `${g.kind ?? '?'} ${g.id ?? ''}`.trim()),
      proof: String(j.writeProof ?? ''),
      endReason: String(j.endReason ?? ''),
      baseVerdict: String(j.baseVerdict ?? ''),
      updated: int(j.updated),
    }
  })
  const blocked: JobBlock[] = (doc.blocked ?? []).map(b => ({
    job: b.job ?? '',
    on: b.on ?? '',
    state: b.state ?? '',
  }))
  return {
    rows,
    overdue: strs(doc.overdue),
    orphans: strs(doc.orphans),
    stale: strs(doc.stale),
    blocked,
    fetchedAt,
  }
}

export function parseRuns(text: string): RunRow[] {
  const doc = JSON.parse(text) as {
    invocations?: { id?: string; command?: { arguments?: string[] }; startTime?: string; endTime?: string; status?: string }[]
  }
  return (doc.invocations ?? []).map(i => ({
    id: i.id ?? '',
    command: (i.command?.arguments ?? []).join(' '),
    startedAt: time(i.startTime),
    endedAt: time(i.endTime),
    status: enumWord(i.status, 'STATUS_'),
  }))
}

export function parseActivity(text: string): ActivityLine[] {
  const doc = JSON.parse(text) as {
    events?: { time?: string; kind?: string; action?: string; outcome?: string; actor?: string; preview?: string }[]
  }
  return (doc.events ?? []).map(e => ({
    at: time(e.time),
    kind: enumWord(e.kind, 'KIND_'),
    action: e.action ?? '',
    outcome: enumWord(e.outcome, 'OUTCOME_'),
    actor: e.actor ?? '',
    preview: e.preview ?? '',
  }))
}

// The jobs a spawn description `<parent>/<role> <job>` can name, in the order the guard
// resolves them: `<job>`, else `<parent>/<job>`. Empty for any other description.
export function candidateJobs(description: string): string[] {
  const words = description.trim().split(/\s+/)
  if (words.length !== 2 || !words[0]!.includes('/')) return []
  const parent = words[0]!.slice(0, words[0]!.lastIndexOf('/'))
  return [words[1]!, `${parent}/${words[1]!}`]
}
