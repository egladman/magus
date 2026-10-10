import type { EngineInterface, ProcessRunResult, Register, TurnUsage } from 'claude-code'

import type { AgentRow, CacheState, Filter, Report, Tab, Ttl } from '../types'
import {
  candidateJobs,
  connectError,
  LIST_ACTIVITY,
  LIST_INVOCATIONS,
  LIST_JOBS,
  parseActivity,
  parseJobs,
  parseRuns,
  socketFromStatus,
} from './api'
import {
  cacheNotice,
  cacheStatus,
  formatTokens,
  formatUsd,
  inferTtl,
  parseRates,
  rateAgeDays,
  requestUsd,
  totalTokens,
} from './cost'
import type { RateTable, Usage } from './cost'
import { assess, signature, statusText } from './health'
import type { BinaryInfo, Probe, ServerInfo } from './health'
import { DEFAULT_RATES } from './rates'
import { readSettings } from './settings'
import type { Settings } from './settings'
import { age, COLOR, GLYPH, leaf, treeLines } from './tree'

const PANE = 'magus'
const HEALTH_GAP_MS = 10_000
const HEALTH_POLL_MS = 120_000
const JOBS_POLL_MS = 15_000
const RUNS_POLL_MS = 5_000
const WATCH_POLL_MS = 3_000
const TICK_MS = 30_000
const MAX_LINES = 300

const REPORT = { plugin: 'magus', key: 'report' } as const
// The signature of the issue set the person hid; the band returns when the set changes.
const DISMISSED = { plugin: 'magus', key: 'dismissed' } as const
const TAB = { plugin: 'magus', key: 'tab' } as const
const LISTING = { plugin: 'magus', key: 'listing' } as const
const RUNS = { plugin: 'magus', key: 'runs' } as const
const ERROR = { plugin: 'magus', key: 'error' } as const
const FILTER = { plugin: 'magus', key: 'filter' } as const
const SELECTED = { plugin: 'magus', key: 'selected' } as const
const WATCHING = { plugin: 'magus', key: 'watching' } as const
const WATCH_LINES = { plugin: 'magus', key: 'watchLines' } as const
const AGENTS = { plugin: 'magus', key: 'agents' } as const
const CACHE = { plugin: 'magus', key: 'cache' } as const
const HELD_FOR = { plugin: 'magus', key: 'heldFor' } as const
const RATES_ERROR = { plugin: 'magus', key: 'ratesError' } as const
const TICK = { plugin: 'magus', key: 'tick' } as const

// Module state, rebuilt by register on every load.
let settings: Settings = readSettings({})
let rates: RateTable = DEFAULT_RATES
// The TTL Claude Code last reported (on a model switch), which outranks an observation.
let engineTtl: Ttl | null = null
let healthInFlight: Promise<void> | null = null
let healthAt = 0
// The server's socket, found once and found again after a call through it fails.
let socket: string | null = null

const usageOf = (u: TurnUsage): Usage => ({
  input: u.input_tokens,
  output: u.output_tokens,
  cacheRead: u.cache_read_input_tokens,
  cacheCreation: u.cache_creation_input_tokens,
})

// ---------- health ----------

async function run($: EngineInterface, argv: string[], cwd: string, timeoutMs = 10_000): Promise<ProcessRunResult | null> {
  try {
    return await $.process.run(argv, { cwd, timeoutMs })
  } catch {
    return null
  }
}

async function binaryInfo($: EngineInterface, path: string, root: string): Promise<BinaryInfo | null> {
  const r = await run($, [path, 'version', '-o', 'json'], root, 5_000)
  if (r?.exitCode !== 0) return null
  try {
    const v = JSON.parse(r.stdout) as { version?: string; commit?: string }
    return { path, version: v.version ?? '', commit: v.commit ?? '' }
  } catch {
    return null
  }
}

async function serverInfo($: EngineInterface, bin: string, root: string): Promise<ServerInfo | null> {
  // Exits 1 with no server running, and still prints the report.
  const r = await run($, [bin, '-s', 'server', 'status', '-o', 'json'], root)
  if (r === null) return null
  try {
    const s = JSON.parse(r.stdout) as {
      server?: unknown
      pool?: { version?: string }
      mcp_endpoint?: { state?: string; note?: string }
    }
    return {
      isRunning: s.server != null,
      version: s.pool?.version ?? '',
      mcpState: s.mcp_endpoint?.state ?? '',
      mcpNote: s.mcp_endpoint?.note ?? '',
    }
  } catch {
    return null
  }
}

export async function probe($: EngineInterface, root: string): Promise<Probe | null> {
  if (!(await $.fs.exists(`${root}/magusfile.buzz`))) return null
  let goMod = ''
  try {
    goMod = await $.fs.read(`${root}/go.mod`)
  } catch {
    // No go.mod, or one that will not read: either way not the magus source tree.
  }
  const isMagusSource = /^module github\.com\/egladman\/magus\s*$/m.test(goMod)
  const hasLocalBinary = await $.fs.exists(`${root}/magus`)

  const which = await run($, ['/bin/sh', '-c', 'command -v magus'], root, 5_000)
  const pathBinary = which?.exitCode === 0 && which.stdout.trim() !== '' ? which.stdout.trim() : null

  const bin = hasLocalBinary ? `${root}/magus` : pathBinary
  const binary = bin === null ? null : await binaryInfo($, bin, root)

  const rev = await run($, ['git', 'rev-parse', 'HEAD'], root, 5_000)
  const head = rev?.exitCode === 0 ? rev.stdout.trim() : null

  let behind: number | null = null
  if (binary?.commit && head && binary.commit !== head) {
    const anc = await run($, ['git', 'merge-base', '--is-ancestor', binary.commit, head], root, 5_000)
    if (anc?.exitCode === 0) {
      const count = await run($, ['git', 'rev-list', '--count', `${binary.commit}..${head}`], root, 5_000)
      behind = count?.exitCode === 0 ? Number(count.stdout.trim()) : null
    }
  }

  const server = bin === null ? null : await serverInfo($, bin, root)
  return { isMagusSource, hasLocalBinary, pathBinary, binary, head, behind, server }
}

// Runs from a timer so the probes (a few seconds of subprocesses) outlive the dispatch.
function scheduleHealth($: EngineInterface): void {
  $.clock.after(0, () => void checkHealth($))
}

async function checkHealth($: EngineInterface): Promise<void> {
  healthInFlight ??= checkHealthOnce($).finally(() => {
    healthInFlight = null
  })
  return healthInFlight
}

async function checkHealthOnce($: EngineInterface): Promise<void> {
  const p = await probe($, await $.session.root())
  healthAt = await $.clock.now()
  const next: Report | null = p === null ? null : { issues: assess(p), checkedAt: healthAt }
  await $.state.set(REPORT, next)
  await refreshStatus($)
}

// ---------- status line ----------

// One entry for the whole mod, which the engine labels with the mod's name: the health
// issues and the cache countdown, or nothing when there is neither.
async function refreshStatus($: EngineInterface): Promise<void> {
  const { value: report = null } = await $.state.get(REPORT)
  const { value: cache = null } = await $.state.get(CACHE)
  const parts = [report ? statusText(report.issues) : undefined, cacheStatus(cache, await $.clock.now())]
  const text = parts.filter(Boolean).join(' · ')
  $.ui.status(text || undefined)
}

// ---------- cost ----------

async function loadRates($: EngineInterface): Promise<void> {
  if (settings.ratesFile === '') {
    rates = DEFAULT_RATES
    await $.state.set(RATES_ERROR, null)
    return
  }
  try {
    rates = parseRates(await $.fs.read(settings.ratesFile))
    await $.state.set(RATES_ERROR, null)
  } catch (err) {
    rates = DEFAULT_RATES
    const why = err instanceof Error ? err.message : String(err)
    await $.state.set(RATES_ERROR, `${settings.ratesFile}: ${why}; using the built-in rates`)
  }
}

function resolveTtl(observed: Ttl | null, previous: CacheState | null): Pick<CacheState, 'ttl' | 'ttlSource'> {
  if (settings.cacheTtl !== 'auto') return { ttl: settings.cacheTtl, ttlSource: 'setting' }
  if (engineTtl !== null) return { ttl: engineTtl, ttlSource: 'engine' }
  if (observed !== null) return { ttl: observed, ttlSource: 'observed' }
  if (previous && previous.ttlSource === 'observed') return { ttl: previous.ttl, ttlSource: 'observed' }
  return { ttl: '5m', ttlSource: 'assumed' }
}

// Records the main conversation's response: when it arrived, what it re-sends next, and
// what its cache hit or miss says about the TTL.
async function recordMainResponse($: EngineInterface, usage: TurnUsage | undefined): Promise<void> {
  const now = await $.clock.now()
  const { value: prev = null } = await $.state.get(CACHE)
  const u = usage ? usageOf(usage) : null
  const observed = u && prev && prev.lastResponseAt > 0 ? inferTtl(now - prev.lastResponseAt, u) : null
  const context = (await $.session.usage()).context.tokens ?? (u ? totalTokens(u) : 0)
  const next: CacheState = {
    lastResponseAt: now,
    contextTokens: context,
    model: usage?.model ?? (await $.session.model()),
    ...resolveTtl(observed, prev),
  }
  await $.state.set(CACHE, next)
  await refreshStatus($)
}

async function recordAgentUsage($: EngineInterface, agentId: string, usage: TurnUsage): Promise<void> {
  const { value: rows = [] } = await $.state.get(AGENTS)
  const row = rows.find(r => r.agentId === agentId)
  if (!row) return
  const { value: cache = null } = await $.state.get(CACHE)
  const u = usageOf(usage)
  const usd = requestUsd(rates, usage.model, u, cache?.ttl ?? '5m')
  const tokens = row.tokens + totalTokens(u)
  const budget = settings.subagentTokenBudget
  if (budget > 0 && row.tokens <= budget && tokens > budget) {
    $.ui.toast(`Subagent "${row.description}" passed its ${formatTokens(budget)}-token budget (${formatTokens(tokens)}).`)
  }
  await $.state.set(
    AGENTS,
    rows.map(r => (r.agentId === agentId ? { ...r, tokens, usd: usd === null || r.usd === null ? null : r.usd + usd } : r)),
  )
}

// ---------- jobs pane ----------

async function magusBin($: EngineInterface, root: string): Promise<string> {
  return (await $.fs.exists(`${root}/magus`)) ? `${root}/magus` : 'magus'
}

// Asks magus where its server listens, the one CLI call the pane makes: the address
// honors server.address, which no client should resolve on its own.
async function discover($: EngineInterface): Promise<string | null> {
  const root = await $.session.root()
  try {
    const r = await $.process.run([await magusBin($, root), '-s', 'server', 'status', '-o', 'json'], {
      cwd: root,
      timeoutMs: 15_000,
    })
    return socketFromStatus(r.stdout)
  } catch {
    return null
  }
}

// One unary Connect call over the server's socket, as the console's transport makes it.
// Resolves the response text, or rejects with a line a person can act on.
async function rpc($: EngineInterface, procedure: string, body: unknown): Promise<string> {
  socket ??= await discover($)
  if (socket === null) {
    throw new Error('No magus server is running. Start one with `magus server start`.')
  }
  try {
    const r = await $.http.fetch(`http://magus${procedure}`, {
      method: 'POST',
      socketPath: socket,
      headers: { 'Content-Type': 'application/json', 'Connect-Protocol-Version': '1' },
      body: JSON.stringify(body),
    })
    if (!r.ok) throw new Error(connectError(r.status, r.text))
    return r.text
  } catch (err) {
    socket = null
    throw err
  }
}

async function isPaneOpen($: EngineInterface): Promise<boolean> {
  return (await $.ui.panes()).some(p => p.id === PANE)
}

async function loadJobs($: EngineInterface): Promise<void> {
  try {
    const text = await rpc($, LIST_JOBS, {})
    await $.state.set(LISTING, parseJobs(text, await $.clock.now()))
    await $.state.set(ERROR, null)
  } catch (err) {
    await $.state.set(ERROR, err instanceof Error ? err.message : String(err))
  }
}

async function loadRuns($: EngineInterface): Promise<void> {
  try {
    const text = await rpc($, LIST_INVOCATIONS, { pageSize: 25 })
    await $.state.set(RUNS, parseRuns(text))
    await $.state.set(ERROR, null)
  } catch (err) {
    await $.state.set(ERROR, err instanceof Error ? err.message : String(err))
  }
}

async function loadWatch($: EngineInterface): Promise<void> {
  const { value: id = null } = await $.state.get(WATCHING)
  if (id === null) return
  try {
    const text = await rpc($, LIST_ACTIVITY, { pageSize: 40, filter: { units: [id] } })
    await $.state.set(WATCH_LINES, parseActivity(text))
  } catch (err) {
    await $.state.set(ERROR, err instanceof Error ? err.message : String(err))
  }
}

async function pollJobs($: EngineInterface): Promise<void> {
  const { value: tab = 'jobs' } = await $.state.get(TAB)
  if (tab === 'jobs' && (await isPaneOpen($))) await loadJobs($)
}

async function pollRuns($: EngineInterface): Promise<void> {
  const { value: tab = 'jobs' } = await $.state.get(TAB)
  if (tab === 'runs' && (await isPaneOpen($))) await loadRuns($)
}

async function pollWatch($: EngineInterface): Promise<void> {
  if (await isPaneOpen($)) await loadWatch($)
}

async function tick($: EngineInterface): Promise<void> {
  const { value: cache = null } = await $.state.get(CACHE)
  if (cache === null) return
  await $.state.set(TICK, await $.clock.now())
  await refreshStatus($)
}

async function showTab($: EngineInterface, tab: Tab): Promise<void> {
  await $.state.set(TAB, tab)
  if (tab === 'jobs') await loadJobs($)
  if (tab === 'runs') await loadRuns($)
}

async function recordSpawn($: EngineInterface, row: AgentRow): Promise<void> {
  const { value: rows = [] } = await $.state.get(AGENTS)
  await $.state.set(AGENTS, [row, ...rows].slice(0, 50))
}

async function recordFinish($: EngineInterface, agentId: string, outcome: string): Promise<void> {
  const { value: rows = [] } = await $.state.get(AGENTS)
  if (!rows.some(r => r.agentId === agentId && r.finishedAt === 0)) return
  const at = await $.clock.now()
  await $.state.set(
    AGENTS,
    rows.map(r => (r.agentId === agentId && r.finishedAt === 0 ? { ...r, finishedAt: at, outcome } : r)),
  )
}

// Opens the first of candidates the job store holds, after a fresh read so a job forked
// since the last poll is found.
async function openJob($: EngineInterface, candidates: string[]): Promise<void> {
  await showTab($, 'jobs')
  const { value: listing = null } = await $.state.get(LISTING)
  const id = candidates.find(c => listing?.rows.some(r => r.id === c)) ?? candidates[0] ?? null
  await $.state.set(SELECTED, id)
  await $.state.set(FILTER, 'all')
}

export const register: Register = (on, options) => {
  settings = readSettings(options)

  on('session.start', async ($, e, next) => {
    await $.command.register({ name: 'magus', description: "Show magus's jobs, runs and this session's agents" })
    await loadRates($)
    scheduleHealth($)
    $.clock.every(HEALTH_POLL_MS, () => void checkHealth($))
    $.clock.every(JOBS_POLL_MS, () => void pollJobs($))
    $.clock.every(RUNS_POLL_MS, () => void pollRuns($))
    $.clock.every(WATCH_POLL_MS, () => void pollWatch($))
    $.clock.every(TICK_MS, () => void tick($))
    return next(e)
  })

  on('command.run', { command: 'magus' }, async $ => {
    await $.ui.open({ id: PANE, title: 'magus', focus: true })
    $.clock.after(0, () => void loadJobs($))
    return { text: 'magus pane opened.' }
  })

  on('turn.complete', async ($, e, next) => {
    if (e.agentId) {
      await recordFinish($, e.agentId, e.isAborted ? 'aborted' : e.reason)
      if (e.usage) await recordAgentUsage($, e.agentId, e.usage)
    } else {
      await recordMainResponse($, e.usage)
      // Catches the rebuild or server restart a turn just made.
      if ((await $.clock.now()) - healthAt >= HEALTH_GAP_MS) scheduleHealth($)
    }
    return next(e)
  })

  on('classic.PostModelSwitch', async ($, e, next) => {
    engineTtl = e.cache_ttl
    const { value: cache = null } = await $.state.get(CACHE)
    if (cache !== null && settings.cacheTtl === 'auto') {
      await $.state.set(CACHE, { ...cache, ttl: e.cache_ttl, ttlSource: 'engine', model: e.to_model })
    }
    return next(e)
  })

  on('classic.SessionStart', async ($, e, next) => {
    if (e.prompt_cache_likely_expired && e.context_tokens) {
      const usd = e.estimated_cache_write_usd
      $.ui.toast(
        `Resumed with an expired prompt cache: the first prompt re-caches ${formatTokens(e.context_tokens)} tokens` +
          (usd === undefined ? '.' : ` (≈${formatUsd(usd)}, Claude Code's estimate).`),
      )
    }
    return next(e)
  })

  on('prompt.submit', async ($, e, next) => {
    if (settings.cacheGate === 'off' || (e.origin && e.origin.kind !== 'composer')) return next(e)
    const { value: cache = null } = await $.state.get(CACHE)
    const notice = cacheNotice(cache, await $.clock.now(), 0, settings.gateMinTokens, rates)
    if (cache === null || notice === null || !notice.isExpired) return next(e)
    if (settings.cacheGate === 'warn') {
      $.ui.toast(notice.text)
      return next(e)
    }
    const { value: heldFor = 0 } = await $.state.get(HELD_FOR)
    if (heldFor === cache.lastResponseAt) return next(e)
    await $.state.set(HELD_FOR, cache.lastResponseAt)
    const text = e.text
    $.clock.after(0, () => void $.prompt.fill({ text }))
    return { drop: `Held: ${notice.text} Submit again to send it.` }
  })

  on('agent.spawn', async ($, e, next) => {
    const r = await next(e)
    if (!r.deny && r.agentId) {
      await recordSpawn($, {
        agentId: r.agentId,
        description: e.description,
        model: r.model,
        isBackground: e.background,
        jobs: candidateJobs(e.description),
        startedAt: await $.clock.now(),
        finishedAt: 0,
        outcome: '',
        tokens: 0,
        usd: 0,
      })
    }
    return r
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    if (e.props.hasSurvey) return next(e)
    await $.state.get(TICK)
    const { value: report = null } = await $.state.get(REPORT)
    const { value: hidden = null } = await $.state.get(DISMISSED)
    const { value: cache = null } = await $.state.get(CACHE)
    const { value: ratesError = null } = await $.state.get(RATES_ERROR)
    const now = await $.clock.now()

    const issues = report && report.issues.length && hidden !== signature(report.issues) ? report.issues : []
    const notice = cacheNotice(cache, now, settings.cacheWarnMs, settings.gateMinTokens, rates)
    const ageDays = rateAgeDays(rates, now)
    const staleRates = notice !== null && ageDays !== null && ageDays > settings.ratesStaleDays
    if (issues.length === 0 && notice === null && ratesError === null) return next(e)

    const { Box, Button, Text } = $.ui.resolve(e)
    const room = Math.max(1, e.props.maxRows - 1)
    const shown = issues.slice(0, room)

    return (
      <Box flexDirection="column">
        {notice !== null ? (
          <Text key="cache" color={notice.isExpired ? 'red' : 'yellow'} wrap="truncate-end">
            ⏳ {notice.text}
            {staleRates ? ` Rates checked ${ageDays} days ago.` : ''}
          </Text>
        ) : null}
        {ratesError !== null ? (
          <Text key="rates" color="red" wrap="truncate-end">
            ✖ {ratesError}
          </Text>
        ) : null}
        {shown.map(i => (
          <Box key={`row-${i.id}`} gap={1}>
            <Box flexShrink={1}>
              <Text color={i.level === 'error' ? 'red' : 'yellow'} wrap="truncate-end">
                {i.level === 'error' ? '✖' : '⚠'} {i.title}
              </Text>
            </Box>
            {i.fix !== undefined ? (
              <Button
                key={`copy-${i.id}`}
                label="Copy fix"
                onPress={p => {
                  void $.ui.copy({ text: i.fix ?? '', surface: p.surface })
                  $.ui.toast(`Copied: ${i.fix}`)
                }}
              />
            ) : null}
          </Box>
        ))}
        {issues.length ? (
          <Box gap={1}>
            <Button key="recheck" label="Recheck" onPress={() => scheduleHealth($)} />
            <Button key="hide" label="Hide" onPress={() => void $.state.set(DISMISSED, signature(issues))} />
          </Box>
        ) : null}
      </Box>
    )
  })

  on('ui.render', { component: 'Pane', requestId: PANE }, async ($, e) => {
    const { Box, Button, Text } = $.ui.resolve(e)
    const { value: tab = 'jobs' } = await $.state.get(TAB)
    const { value: error = null } = await $.state.get(ERROR)
    const now = await $.clock.now()

    const tabButton = (t: Tab, label: string, hotkey: string) => (
      <Button
        key={`tab-${t}`}
        label={label}
        hotkey={hotkey}
        variant={tab === t ? 'primary' : 'secondary'}
        onPress={() => $.clock.after(0, () => void showTab($, t))}
      />
    )
    const header = (
      <Box gap={1}>
        {tabButton('jobs', 'Jobs', '1')}
        {tabButton('runs', 'Runs', '2')}
        {tabButton('agents', 'Agents', '3')}
      </Box>
    )
    const errorLine = error !== null ? <Text color="red" wrap="wrap">{error}</Text> : null

    if (tab === 'agents') {
      const { value: agents = [] } = await $.state.get(AGENTS)
      const budget = settings.subagentTokenBudget
      return (
        <Box flexDirection="column">
          {header}
          {agents.length === 0 ? <Text dimColor>No subagents spawned in this session yet.</Text> : null}
          {agents.map(a => {
            const over = budget > 0 && a.tokens > budget
            return (
              <Box key={`agent-${a.agentId}`} gap={1}>
                <Text color={a.finishedAt === 0 ? 'cyan' : a.outcome === 'answer' ? 'green' : 'yellow'}>
                  {a.finishedAt === 0 ? '●' : a.outcome === 'answer' ? '✓' : '◐'}
                </Text>
                <Text wrap="truncate-end">{a.description}</Text>
                <Text dimColor>
                  {[
                    a.model,
                    a.isBackground ? 'background' : '',
                    age(((a.finishedAt || now) - a.startedAt) / 1000),
                    a.tokens ? `${formatTokens(a.tokens)} tokens` : '',
                    a.tokens && a.usd !== null ? formatUsd(a.usd) : '',
                  ]
                    .filter(Boolean)
                    .join(' · ')}
                </Text>
                {over ? <Text color="yellow">over {formatTokens(budget)} budget</Text> : null}
                {a.jobs.length ? (
                  <Button
                    key={`agent-job-${a.agentId}`}
                    plain
                    label={`job ${a.jobs[0]}`}
                    onPress={() => $.clock.after(0, () => void openJob($, a.jobs))}
                  />
                ) : null}
              </Box>
            )
          })}
        </Box>
      )
    }

    if (tab === 'runs') {
      const { value: runs = null } = await $.state.get(RUNS)
      return (
        <Box flexDirection="column">
          {header}
          {errorLine}
          {runs === null && error === null ? <Text dimColor>reading…</Text> : null}
          {(runs ?? []).map(r => {
            const running = r.endedAt === 0
            const failed = r.status === 'fail'
            return (
              <Box key={`run-${r.id}`} gap={1}>
                <Text color={running ? 'cyan' : failed ? 'red' : 'green'}>{running ? '●' : failed ? '✗' : '✓'}</Text>
                <Text wrap="truncate-end">magus {r.command}</Text>
                <Text dimColor>
                  {running
                    ? `${age((now - r.startedAt) / 1000)} so far`
                    : `${age((r.endedAt - r.startedAt) / 1000)} · ${age((now - r.endedAt) / 1000)} ago`}
                </Text>
                <Button key={`copy-${r.id}`} plain label={r.id} onPress={p => void $.ui.copy({ text: r.id, surface: p.surface })} />
              </Box>
            )
          })}
        </Box>
      )
    }

    const { value: listing = null } = await $.state.get(LISTING)
    const { value: filter = 'live' } = await $.state.get(FILTER)
    const { value: selected = null } = await $.state.get(SELECTED)
    const { value: watching = null } = await $.state.get(WATCHING)
    const { value: watchLines = [] } = await $.state.get(WATCH_LINES)
    const { value: agents = [] } = await $.state.get(AGENTS)

    const lines = listing ? treeLines(listing.rows, filter, now / 1000, new Set(listing.stale)) : []
    const hiddenStale = filter === 'live' && listing ? listing.stale.length : 0
    const shown = lines.slice(0, MAX_LINES)
    const picked = listing?.rows.find(r => r.id === selected) ?? null
    const flags = (id: string): string[] => {
      if (!listing) return []
      const out: string[] = []
      if (listing.overdue.includes(id)) out.push('overdue')
      if (listing.stale.includes(id)) out.push('stale')
      if (listing.orphans.includes(id)) out.push('orphaned')
      const b = listing.blocked.find(x => x.job === id)
      if (b) out.push(`blocked on ${b.on}`)
      if (agents.some(a => a.jobs.includes(id) && a.finishedAt === 0)) out.push('agent running')
      return out
    }

    const filterButton = (f: Filter, label: string, hotkey: string) => (
      <Button
        key={`filter-${f}`}
        label={label}
        hotkey={hotkey}
        variant={filter === f ? 'primary' : 'secondary'}
        onPress={() => void $.state.set(FILTER, f)}
      />
    )

    return (
      <Box flexDirection="column">
        {header}
        <Box gap={1}>
          {filterButton('live', 'Live', 'l')}
          {filterButton('recent', 'Today', 't')}
          {filterButton('all', 'All', 'a')}
          <Text dimColor wrap="truncate-end">
            {listing
              ? [
                  `${lines.length} shown of ${listing.rows.length}`,
                  hiddenStale ? `${hiddenStale} stale hidden` : '',
                  `${age((now - listing.fetchedAt) / 1000)} ago`,
                ]
                  .filter(Boolean)
                  .join(' · ')
              : 'reading…'}
          </Text>
        </Box>
        {errorLine}
        {listing && lines.length === 0 ? <Text dimColor>{filter === 'live' ? 'No live jobs.' : 'No jobs match.'}</Text> : null}
        {shown.map(({ row, depth, isContext }) => {
          const f = flags(row.id)
          return (
            <Box key={`line-${row.id}`} gap={1}>
              <Text>{'  '.repeat(depth)}</Text>
              <Text color={COLOR[row.state]} dimColor={isContext}>
                {GLYPH[row.state]}
              </Text>
              <Button
                key={`sel-${row.id}`}
                plain
                dimColor={isContext}
                label={leaf(row.id, row.parent)}
                onPress={() => void $.state.set(SELECTED, selected === row.id ? null : row.id)}
              />
              <Text dimColor wrap="truncate-end">
                {[row.state, row.model, row.holder, `${age(now / 1000 - row.updated)} ago`].filter(Boolean).join(' · ')}
              </Text>
              {f.length ? <Text color="yellow">{f.join(' · ')}</Text> : null}
            </Box>
          )
        })}
        {lines.length > shown.length ? <Text dimColor>… {lines.length - shown.length} more; narrow the filter</Text> : null}
        {picked ? (
          <Box flexDirection="column" marginTop={1} borderStyle="round" paddingX={1}>
            <Text bold wrap="truncate-end">
              {GLYPH[picked.state]} {picked.id}
            </Text>
            {picked.criteria ? <Text wrap="wrap">{picked.criteria}</Text> : null}
            {picked.check ? <Text dimColor wrap="truncate-end">check: {picked.check}</Text> : null}
            {picked.writePaths.length ? (
              <Text dimColor wrap="truncate-end">
                writes: {picked.writePaths.slice(0, 6).join(', ')}
                {picked.writePaths.length > 6 ? ` +${picked.writePaths.length - 6}` : ''}
              </Text>
            ) : null}
            {picked.goals.length ? <Text dimColor wrap="truncate-end">goals: {picked.goals.join(', ')}</Text> : null}
            {picked.proof || picked.baseVerdict ? (
              <Text dimColor>
                {[picked.proof && `write proof: ${picked.proof}`, picked.baseVerdict && `base: ${picked.baseVerdict}`]
                  .filter(Boolean)
                  .join(' · ')}
              </Text>
            ) : null}
            {picked.endReason ? <Text color="yellow" wrap="wrap">ended: {picked.endReason}</Text> : null}
            <Box gap={1}>
              {watching === picked.id ? (
                <Button key="unwatch" label="Stop watching" hotkey="w" onPress={() => void $.state.set(WATCHING, null)} />
              ) : (
                <Button
                  key="watch"
                  label="Watch"
                  hotkey="w"
                  onPress={() =>
                    $.clock.after(0, async () => {
                      await $.state.set(WATCHING, picked.id)
                      await $.state.set(WATCH_LINES, [])
                      await loadWatch($)
                    })
                  }
                />
              )}
              <Button
                key="copy-describe"
                label="Copy describe"
                onPress={p => void $.ui.copy({ text: `magus describe job ${picked.id}`, surface: p.surface })}
              />
            </Box>
            {watching === picked.id ? (
              watchLines.length ? (
                watchLines.map((l, i) => (
                  <Text
                    key={`w-${i}`}
                    dimColor={l.outcome !== 'error'}
                    color={l.outcome === 'error' ? 'red' : undefined}
                    wrap="truncate-end"
                  >
                    {new Date(l.at).toLocaleTimeString()} {l.kind} {l.action} {l.preview}
                  </Text>
                ))
              ) : (
                <Text dimColor>No activity recorded under this job yet.</Text>
              )
            ) : null}
          </Box>
        ) : null}
      </Box>
    )
  })
}
