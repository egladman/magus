import { expect, mock, test } from 'claude-code/testing'
import type { On } from 'claude-code'

const ROOT = '/w'
const SOCK = '/c/magus/run/server.sock'
const MIN = 60_000
const T0 = 1_000_000_000

const BAND = {
  plugin: 'magus',
  surface: 'terminal',
  component: 'AbovePrompt',
  props: { hasSurvey: false, isWorking: false, maxRows: 6, bodyColumns: 120, scroll: { offset: 0, bodyRows: 6 }, view: {} },
} as const
const PANE = {
  plugin: 'magus',
  surface: 'desktop',
  component: 'Pane',
  requestId: 'magus',
  props: { title: 'magus', isFocused: true, bodyColumns: 100, placement: 'dock', scroll: { offset: 0, bodyRows: 30 }, view: {} },
} as const
const RUN_MAGUS = {
  command: 'magus',
  args: '',
  origin: { kind: 'composer' },
  presentation: { isFullscreen: false, columns: 100 },
} as const

type Seen = { statuses: (string | undefined)[]; toasts: string[]; fills: string[] }
type Opts = {
  workspace?: boolean
  serverUp?: boolean
  files?: Record<string, string>
  run?: (argv: readonly string[]) => { exitCode: number; stdout: string } | undefined
}

const ok = (exitCode: number, stdout: string) => ({
  value: { exitCode, stdout, stderr: '', isStdoutTruncated: false, isStderrTruncated: false },
})

function engine(on: On, opts: Opts = {}): Seen {
  const seen: Seen = { statuses: [], toasts: [], fills: [] }
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  on('session.root', () => ({ value: ROOT }))
  on('session.model', () => ({ value: 'claude-opus-5-5' }))
  on('session.usage', () => ({ value: { startedAt: 0, context: { tokens: 182_000, window: 1_000_000 }, rateLimits: [] } as never }))
  on('command.register', ($, e) => ({ value: { command: e.name } }))
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('ui.panes', () => ({ value: [{ id: 'magus', title: 'magus', isFocused: true }] as never }))
  on('ui.status', ($, e) => {
    seen.statuses.push(e.text)
    return { value: undefined }
  })
  on('ui.toast', ($, e) => {
    seen.toasts.push(e.text)
    return { value: undefined }
  })
  on('prompt.fill', ($, e) => {
    seen.fills.push(e.text)
    return { isFilled: true } as never
  })
  on('prompt.submit', ($, e) => ({ text: e.text }))
  on('turn.complete', () => ({ text: 'done' }) as never)
  on('agent.spawn', () => ({ model: 'claude-haiku-5-5', agentId: 'ag1' }))
  on('fs.exists', ($, e) => ({ value: opts.workspace === true && [`${ROOT}/magusfile.buzz`, `${ROOT}/go.mod`, `${ROOT}/magus`].includes(e.path) }))
  on('fs.read', ($, e) => {
    const text = opts.files?.[e.path]
    if (text !== undefined) return { value: text }
    if (e.path === `${ROOT}/go.mod`) return { value: 'module github.com/egladman/magus\n' }
    return { deny: `no such file: ${e.path}` } as never
  })
  on('process.run', ($, e) => {
    const custom = opts.run?.(e.argv)
    if (custom) return ok(custom.exitCode, custom.stdout)
    const cmd = e.argv.join(' ')
    if (cmd.endsWith('server status -o json')) {
      return opts.serverUp
        ? ok(0, JSON.stringify({ server: { pid: 1 }, pool: { socket: `unix://${SOCK}`, version: 'v1' }, mcp_endpoint: { state: 'serving' } }))
        : ok(1, JSON.stringify({ pool_error: 'no such file' }))
    }
    return ok(127, '')
  })
  on('http.fetch', ($, e) => {
    const answers: Record<string, unknown> = {
      'http://magus/magus.job.v1alpha1.JobService/ListJobs': {
        jobs: [{ id: 'css-reorg', state: 'running', model: 'sonnet', criteria: 'one stylesheet per app', updated: String(T0 / 1000) }],
      },
    }
    return { value: { status: 200, ok: true, headers: {}, text: JSON.stringify(answers[e.url] ?? {}) } }
  })
  on('ui.render', ($, e) => {
    const { Text } = $.ui.resolve(e)
    return <Text>engine</Text>
  })
  return seen
}

const usage = (u: Partial<{ input: number; output: number; read: number; write: number }>, model = 'claude-opus-5-5') => ({
  model,
  input_tokens: u.input ?? 100,
  output_tokens: u.output ?? 500,
  cache_read_input_tokens: u.read ?? 0,
  cache_creation_input_tokens: u.write ?? 0,
})

const main = (u: ReturnType<typeof usage>) =>
  ({ answer: 'ok', durationMs: 1000, isAborted: false, turnId: 't', reason: 'answer', usage: u }) as never

test('the status line stays empty until a response, then counts down the cache', async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  const seen = engine(on)
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
  await clock.advance(1)
  await clock.settle()
  expect(seen.statuses.at(-1)).toBeUndefined()

  await $.turn.complete(main(usage({ write: 182_000 })))
  expect(seen.statuses.at(-1)).toBe('cache 5m')
  await clock.advance(3 * MIN)
  await clock.settle()
  expect(seen.statuses.at(-1)).toBe('cache 2m')
})

test('the band warns inside the window, and the warn gate toasts after expiry but sends', async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  const seen = engine(on)
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
  await $.turn.complete(main(usage({ write: 182_000 })))

  const ui = await $.ui.mount(BAND)
  expect(await ui.find({ type: 'Text', text: 'engine' })).toBeDefined()

  await clock.advance(2 * MIN)
  await clock.settle()
  expect(await ui.find({ type: 'Text', text: 'engine' })).toBeDefined()

  await clock.advance(1.5 * MIN)
  await clock.settle()
  expect(await ui.find({ type: 'Text', text: /Cache expires in 1m: after that your next prompt re-caches 182K tokens \(≈\$0\.91\)/ })).toBeDefined()

  await clock.advance(2.5 * MIN)
  await clock.settle()
  expect(await ui.find({ type: 'Text', text: /Cache expired 1m ago/ })).toBeDefined()

  const sent = await $.prompt.submit({ text: 'next step' } as never)
  expect(sent).toMatchObject({ text: 'next step' })
  expect(seen.toasts.at(-1)).toMatch(/Cache expired 1m ago/)
})

test('the confirm gate holds an expired prompt once, puts the text back, and sends it the second time', { options: { cacheGate: 'confirm' } }, async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  const seen = engine(on)
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
  await $.turn.complete(main(usage({ write: 182_000 })))
  await clock.advance(10 * MIN)
  await clock.settle()

  const held = await $.prompt.submit({ text: 'refactor the store' } as never)
  expect(held).toMatchObject({ drop: expect.stringMatching(/^Held: Cache expired 5m ago.*Submit again to send it\.$/) })
  await clock.advance(1)
  await clock.settle()
  expect(seen.fills).toEqual(['refactor the store'])

  expect(await $.prompt.submit({ text: 'refactor the store' } as never)).toMatchObject({ text: 'refactor the store' })
})

test('a small context passes the gate silently', { options: { cacheGate: 'confirm', gateMinTokens: 500_000 } }, async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  const seen = engine(on)
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
  await $.turn.complete(main(usage({ write: 182_000 })))
  await clock.advance(10 * MIN)
  await clock.settle()
  expect(await $.prompt.submit({ text: 'hi' } as never)).toMatchObject({ text: 'hi' })
  expect(seen.toasts).toEqual([])
})

test('a cache hit after a 20-minute gap teaches the mod the TTL is an hour', async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  const seen = engine(on)
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
  await $.turn.complete(main(usage({ write: 182_000 })))
  await clock.advance(20 * MIN)
  await clock.settle()
  expect(seen.statuses.at(-1)).toBe('cache expired')

  await $.turn.complete(main(usage({ read: 182_000, write: 300 })))
  expect(seen.statuses.at(-1)).toBe('cache 1h0m')
})

test('a subagent past its token budget is toasted once and flagged with its cost', async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  const seen = engine(on)
  await $.session.start({ cwd: ROOT, surface: 'desktop', isInteractive: true })
  await $.agent.spawn({
    tool_use_id: 't1',
    prompt: 'read the store',
    description: 'css-reorg/scout tokens',
    subagentType: 'Explore',
    provider: { plugin: 'engine' },
    parentModel: 'claude-opus-5-5',
    background: true,
  } as never)
  const sub = (u: ReturnType<typeof usage>) =>
    ({ answer: 'done', durationMs: 1000, isAborted: false, turnId: 's', reason: 'answer', agentId: 'ag1', usage: u }) as never
  await $.turn.complete(sub(usage({ input: 60_000, output: 2_000 }, 'claude-haiku-5-5')))
  await $.turn.complete(sub(usage({ input: 60_000, output: 2_000 }, 'claude-haiku-5-5')))
  expect(seen.toasts).toEqual(['Subagent "css-reorg/scout tokens" passed its 100K-token budget (124K).'])

  await $.command.run(RUN_MAGUS)
  const ui = await $.ui.mount(PANE)
  await ui.press({ key: 'tab-agents' })
  await clock.advance(1)
  await clock.settle()
  expect(await ui.find({ type: 'Text', text: /124K tokens · \$0\.01/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: 'over 100K budget' })).toBeDefined()
})

test('a rates file that does not parse falls back to the built-in rates and says so', { options: { ratesFile: '/r.json' } }, async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  engine(on, { files: { '/r.json': '{"models": "nope"}' } })
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
  const ui = await $.ui.mount(BAND)
  expect(await ui.find({ type: 'Text', text: /\/r\.json: rates: cacheWriteMultiplier.*using the built-in rates/ })).toBeDefined()
})

test('/magus reads ListJobs over the server socket and opens a job on press', async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0 + MIN)
  engine(on, { serverUp: true })
  await $.session.start({ cwd: ROOT, surface: 'desktop', isInteractive: true })
  await $.command.run(RUN_MAGUS)
  await clock.advance(1)
  await clock.settle()
  const ui = await $.ui.mount(PANE)
  await ui.press({ key: 'sel-css-reorg' })
  expect(await ui.find({ type: 'Text', text: 'one stylesheet per app' })).toBeDefined()
})

test('health issues join the cache in one status entry and draw in the band', async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  const seen = engine(on, {
    workspace: true,
    run: argv => {
      const cmd = argv.join(' ')
      if (cmd.endsWith('command -v magus')) return { exitCode: 0, stdout: '/usr/local/bin/magus\n' }
      if (cmd === `${ROOT}/magus version -o json`) return { exitCode: 0, stdout: '{"version":"v1","commit":"aaa"}' }
      if (cmd === 'git rev-parse HEAD') return { exitCode: 0, stdout: 'aaa\n' }
      return undefined
    },
  })
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
  await clock.advance(1)
  await clock.settle()
  expect(seen.statuses.at(-1)).toBe('⚠ server down')

  await $.turn.complete(main(usage({ write: 182_000 })))
  expect(seen.statuses.at(-1)).toBe('⚠ server down · cache 5m')
  const ui = await $.ui.mount(BAND)
  expect(await ui.find({ type: 'Text', text: /No magus server/ })).toBeDefined()
  await ui.press({ key: 'hide' })
  expect(await ui.find({ type: 'Text', text: /No magus server/ })).toBeUndefined()
})
