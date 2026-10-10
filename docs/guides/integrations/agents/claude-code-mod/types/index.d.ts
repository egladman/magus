export type Level = 'error' | 'warn'

export type Issue = {
  id: string
  level: Level
  label: string
  title: string
  fix?: string
}

export type Report = { issues: Issue[]; checkedAt: number }

export type JobState = 'declared' | 'running' | 'exited' | 'pass' | 'fail' | 'no_return'

export type JobRow = {
  id: string
  parent: string
  state: JobState
  model: string
  holder: string
  criteria: string
  check: string
  writePaths: string[]
  goals: string[]
  proof: string
  endReason: string
  baseVerdict: string
  // Unix seconds.
  updated: number
}

export type JobBlock = { job: string; on: string; state: string }

// One JobService.ListJobs answer, cut to what the pane draws.
export type Listing = {
  rows: JobRow[]
  overdue: string[]
  orphans: string[]
  stale: string[]
  blocked: JobBlock[]
  // Unix milliseconds of the read.
  fetchedAt: number
}

// One ViewerService invocation. Times are unix milliseconds; endedAt is 0 while running.
export type RunRow = {
  id: string
  command: string
  startedAt: number
  endedAt: number
  status: string
}

// One ActivityService event under a watched job's lease.
export type ActivityLine = {
  at: number
  kind: string
  action: string
  outcome: string
  actor: string
  preview: string
}

// One subagent this session spawned. Times are unix milliseconds.
export type AgentRow = {
  agentId: string
  description: string
  model: string
  isBackground: boolean
  // The jobs the spawn description can name, in the order the guard resolves them.
  jobs: string[]
  startedAt: number
  finishedAt: number
  outcome: string
  // input + output + cache read + cache write tokens across the subagent's turns.
  tokens: number
  // null while its model has no rates.
  usd: number | null
}

export type Filter = 'live' | 'recent' | 'all'

export type Tab = 'jobs' | 'runs' | 'agents'

export type Ttl = '5m' | '1h'

// What the mod knows about the main conversation's prompt cache.
export type CacheState = {
  // Unix milliseconds the last main-thread response arrived; 0 before the first.
  lastResponseAt: number
  ttl: Ttl
  // setting: chosen in the mod's settings; engine: reported by Claude Code; observed:
  // inferred from a cache hit or miss; assumed: nothing has said yet.
  ttlSource: 'setting' | 'engine' | 'observed' | 'assumed'
  // Prompt tokens the next request re-sends.
  contextTokens: number
  model: string
}

declare module 'claude-code' {
  interface PluginState {
    magus: {
      report: Report | null
      dismissed: string | null
      tab: Tab
      listing: Listing | null
      runs: RunRow[] | null
      error: string | null
      filter: Filter
      selected: string | null
      watching: string | null
      watchLines: ActivityLine[]
      agents: AgentRow[]
      cache: CacheState | null
      // The lastResponseAt a held prompt was held for; the next submit for it goes through.
      heldFor: number
      ratesError: string | null
      // Bumped on a timer so the countdown in the band and status line redraws.
      tick: number
    }
  }
}
