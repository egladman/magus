import { describe, expect, mock, test } from 'claude-code/testing'
import type { Engine } from 'claude-code/testing'
import type { On } from 'claude-code'

import { dayKey } from '../hooks/cost'

const ROOT = '/w'
const SOCK = '/c/magus/run/server.sock'
const TOKEN = 'f00d'
const MIN = 60_000
const T0 = 1_000_000_000

const BAND = {
  plugin: 'magus',
  surface: 'terminal',
  component: 'AbovePrompt',
  props: {
    hasSurvey: false,
    isWorking: false,
    maxRows: 6,
    bodyColumns: 120,
    scroll: { offset: 0, bodyRows: 6 },
    view: {},
  },
} as const
const PANE = {
  plugin: 'magus',
  surface: 'desktop',
  component: 'Pane',
  requestId: 'magus',
  props: {
    title: 'magus',
    isFocused: true,
    bodyColumns: 100,
    placement: 'dock',
    scroll: { offset: 0, bodyRows: 30 },
    view: {},
  },
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
  noLocalBinary?: boolean
  noClipboard?: boolean
  serverUp?: boolean
  files?: Record<string, string>
  env?: Record<string, string>
  // The prompt tokens the session reports; 182K unless a test needs another.
  context?: number
  // The model the engine resolves a spawn to; haiku unless a test needs another.
  spawnedModel?: string
  // Claude Code's session total, as /cost has it; none unless a test sets it.
  costUsd?: () => number | undefined
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
  on('session.usage', () => {
    const usd = opts.costUsd?.()
    const context = { tokens: opts.context ?? 182_000, window: 1_000_000 }
    return {
      value: { startedAt: 0, context, rateLimits: [], ...(usd === undefined ? {} : { cost: { usd } }) } as never,
    }
  })
  on('env.get', ($, e) => ({ value: opts.env?.[e.name] }))
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
  on('agent.spawn', () => ({ model: opts.spawnedModel ?? 'claude-haiku-5-5', agentId: 'ag1' }))
  on('fs.exists', ($, e) => ({
    value:
      opts.workspace === true &&
      [`${ROOT}/magusfile.buzz`, `${ROOT}/go.mod`, ...(opts.noLocalBinary ? [] : [`${ROOT}/magus`])].includes(e.path),
  }))
  on(
    'ui.copy',
    () => ({ value: opts.noClipboard ? { isCopied: false, reason: 'no-clipboard' } : { isCopied: true } }) as never,
  )
  on('fs.read', ($, e) => {
    const text = opts.files?.[e.path]
    if (text !== undefined) return { value: text }
    if (e.path === `${ROOT}/go.mod`) return { value: 'module github.com/egladman/magus\n' }
    if (opts.serverUp && e.path === `${SOCK}.token`) return { value: `${TOKEN}\n` }
    return { deny: `no such file: ${e.path}` } as never
  })
  on('process.run', ($, e) => {
    const custom = opts.run?.(e.argv)
    if (custom) return ok(custom.exitCode, custom.stdout)
    const cmd = e.argv.join(' ')
    if (cmd.endsWith('server status -o json')) {
      return opts.serverUp
        ? ok(
            0,
            JSON.stringify({
              server: { pid: 1 },
              pool: { socket: `unix://${SOCK}`, version: 'v1' },
              mcp_endpoint: { state: 'serving' },
            }),
          )
        : ok(1, JSON.stringify({ pool_error: 'no such file' }))
    }
    return ok(127, '')
  })
  on('http.fetch', ($, e) => {
    if (e.init?.headers?.['Magus-Proc-Token'] !== TOKEN) {
      return { value: { status: 401, ok: false, headers: {}, text: '{"message":"proc: token refused"}' } }
    }
    const answers: Record<string, unknown> = {
      'http://magus/magus.job.v1alpha1.JobService/ListJobs': {
        jobs: [
          {
            id: 'css-reorg',
            state: 'running',
            model: 'sonnet',
            criteria: 'one stylesheet per app',
            updated: String(T0 / 1000),
          },
        ],
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

const usage = (
  u: Partial<{ input: number; output: number; read: number; write: number }>,
  model = 'claude-opus-5-5',
) => ({
  model,
  input_tokens: u.input ?? 100,
  output_tokens: u.output ?? 500,
  cache_read_input_tokens: u.read ?? 0,
  cache_creation_input_tokens: u.write ?? 0,
})

const main = (u: ReturnType<typeof usage>) =>
  ({ answer: 'ok', durationMs: 1000, isAborted: false, turnId: 't', reason: 'answer', usage: u }) as never

test('the status line shows only cost and the band stays empty while a cache counts down', async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  const seen = engine(on)
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
  await $.turn.complete(main(usage({ write: 182_000 })))
  await clock.advance(4 * MIN)
  await clock.settle()
  expect(seen.statuses.at(-1)).toMatch(/^≈\$\d+\.\d\d session \(mod rates\)$/)
  const ui = await $.ui.mount(BAND)
  expect(await ui.find({ type: 'Text', text: 'engine' })).toBeDefined()
})

test("the status line shows Claude Code's session total and today's, and the day adds each delta", async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  let usd = 1.25
  // Another session already spent $10 today.
  mock.store(on, { [dayKey(T0)]: 10 })
  const seen = engine(on, { costUsd: () => usd })
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })

  await $.turn.complete(main(usage({ write: 182_000 })))
  expect(seen.statuses.at(-1)).toBe('≈$1.25 session · ≈$11.25 today')

  // The day is read back from the store, so $12 proves $11.25 was written.
  usd = 2
  await $.turn.complete(main(usage({ read: 182_000 })))
  expect(seen.statuses.at(-1)).toBe('≈$2.00 session · ≈$12.00 today')
})

test(
  "the budget gate counts Claude Code's total where it reports one",
  { options: { sessionBudgetUsd: 1 } },
  async ($, on) => {
    const clock = mock.clock(on)
    await clock.set(T0)
    engine(on, { costUsd: () => 1.5 })
    await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
    await $.turn.complete(main(usage({ read: 1_000 })))

    const held = await $.prompt.submit({ text: 'go on' } as never)
    expect(held).toMatchObject({ drop: expect.stringMatching(/Session spend \$1\.50 has passed your \$1\.00 budget/) })
  },
)

test(
  'the warn gate toasts once at submit after expiry and still sends; a second submit before the next response says nothing',
  { options: { cacheGate: 'warn' } },
  async ($, on) => {
    const clock = mock.clock(on)
    await clock.set(T0)
    const seen = engine(on)
    await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
    await $.turn.complete(main(usage({ write: 182_000 })))
    await clock.advance(6 * MIN)
    await clock.settle()

    const sent = await $.prompt.submit({ text: 'next step' } as never)
    expect(sent).toMatchObject({ text: 'next step' })
    expect(seen.toasts.at(-1)).toMatch(/Cache expired 1m ago/)
    const toasts = seen.toasts.length

    const queued = await $.prompt.submit({ text: 'one more thing' } as never)
    expect(queued).toMatchObject({ text: 'one more thing' })
    expect(seen.toasts).toHaveLength(toasts)
  },
)

test('the cache gate holds by default', async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  engine(on)
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
  await $.turn.complete(main(usage({ write: 182_000 })))
  await clock.advance(6 * MIN)
  await clock.settle()

  expect(await $.prompt.submit({ text: 'next step' } as never)).toMatchObject({
    drop: expect.stringMatching(/^Held: /),
  })
})

test(
  'the confirm gate holds an expired prompt once, puts the text back, and sends it the second time',
  { options: { cacheGate: 'confirm' } },
  async ($, on) => {
    const clock = mock.clock(on)
    await clock.set(T0)
    const seen = engine(on)
    await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
    await $.turn.complete(main(usage({ write: 182_000 })))
    await clock.advance(10 * MIN)
    await clock.settle()

    const held = await $.prompt.submit({ text: 'refactor the store' } as never)
    expect(held).toMatchObject({
      drop: expect.stringMatching(/^Held: Cache expired 5m ago.*Submit again to send it\.$/),
    })
    await clock.advance(1)
    await clock.settle()
    expect(seen.fills).toEqual(['refactor the store'])

    expect(await $.prompt.submit({ text: 'refactor the store' } as never)).toMatchObject({ text: 'refactor the store' })
  },
)

test(
  'a small context passes the gate silently',
  { options: { cacheGate: 'confirm', gateMinTokens: 500_000 } },
  async ($, on) => {
    const clock = mock.clock(on)
    await clock.set(T0)
    const seen = engine(on)
    await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
    await $.turn.complete(main(usage({ write: 182_000 })))
    await clock.advance(10 * MIN)
    await clock.settle()
    expect(await $.prompt.submit({ text: 'hi' } as never)).toMatchObject({ text: 'hi' })
    expect(seen.toasts).toEqual([])
  },
)

test(
  'a cache hit after a 20-minute gap teaches the mod the TTL is an hour',
  { options: { cacheGate: 'warn' } },
  async ($, on) => {
    const clock = mock.clock(on)
    await clock.set(T0)
    const seen = engine(on)
    await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
    await $.turn.complete(main(usage({ write: 182_000 })))
    await clock.advance(20 * MIN)
    await clock.settle()

    await $.turn.complete(main(usage({ read: 182_000, write: 300 })))
    await clock.advance(61 * MIN)
    await clock.settle()
    await $.prompt.submit({ text: 'late' } as never)
    expect(seen.toasts.at(-1)).toMatch(/Cache expired 1m ago.*TTL 1h, observed/)
  },
)

test('a subagent shows its cost in the pane and raises no toast', async ($, on) => {
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
    ({
      answer: 'done',
      durationMs: 1000,
      isAborted: false,
      turnId: 's',
      reason: 'answer',
      agentId: 'ag1',
      usage: u,
    }) as never
  await $.turn.complete(sub(usage({ input: 60_000, output: 2_000 }, 'claude-haiku-5-5')))
  await $.turn.complete(sub(usage({ input: 60_000, output: 2_000 }, 'claude-haiku-5-5')))
  expect(seen.toasts).toEqual([])

  await $.command.run(RUN_MAGUS)
  const ui = await $.ui.mount(PANE)
  await ui.press({ key: 'tab-agents' })
  await clock.advance(1)
  await clock.settle()
  expect(await ui.find({ type: 'Text', text: /124K tokens · \$0\.01/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /over .* budget/ })).toBeUndefined()
})

test(
  'a rates file that does not parse falls back to the built-in rates and says so',
  { options: { ratesFile: '/r.json' } },
  async ($, on) => {
    const clock = mock.clock(on)
    await clock.set(T0)
    engine(on, { files: { '/r.json': '{"models": "nope"}' } })
    await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
    const ui = await $.ui.mount(BAND)
    expect(
      await ui.find({ type: 'Text', text: /\/r\.json: rates: cacheWriteMultiplier.*using the built-in rates/ }),
    ).toBeDefined()
  },
)

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

test('a server that is down shows in the status line and not in the band', async ($, on) => {
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
  expect(seen.statuses.at(-1)).toMatch(/^⚠ server down · ≈\$\d+\.\d\d session \(mod rates\)$/)
  const ui = await $.ui.mount(BAND)
  expect(await ui.find({ type: 'Text', text: /No magus server/ })).toBeUndefined()
  expect(await ui.find({ type: 'Button', text: /Hide|Recheck/ })).toBeUndefined()
})

const HAIKU = 'claude-haiku-5-5'

test('the price step gate holds past the step by default', async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  engine(on)
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
  await $.turn.complete(main(usage({ write: 182_000 }, HAIKU)))

  expect(await $.prompt.submit({ text: 'go on' } as never)).toMatchObject({ drop: expect.stringMatching(/^Held: /) })
})

test(
  'the warn price step gate toasts past the step and still sends',
  { options: { priceStepGate: 'warn' } },
  async ($, on) => {
    const clock = mock.clock(on)
    await clock.set(T0)
    const seen = engine(on)
    await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
    await $.turn.complete(main(usage({ write: 182_000 }, HAIKU)))

    expect(await $.prompt.submit({ text: 'go on' } as never)).toMatchObject({ text: 'go on' })
    expect(seen.toasts).toEqual([
      'Context is 182K tokens, past the 100K step: every request on claude-haiku-5-5 now pays $0.50 per MTok input and $2.50 output, up from $0.10 and $0.50. Consider /compact.',
    ])
  },
)

test('the price step gate speaks inside its margin', { options: { priceStepGate: 'warn' } }, async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  const seen = engine(on, { context: 95_000 })
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
  await $.turn.complete(main(usage({ write: 95_000 }, HAIKU)))
  await $.prompt.submit({ text: 'go on' } as never)
  expect(seen.toasts).toHaveLength(1)
  expect(seen.toasts[0]).toMatch(/5K under the 100K step where claude-haiku-5-5 rises from \$0\.10 to \$0\.50/)
})

test('the price step gate stays quiet further under the step than its margin', async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  const seen = engine(on, { context: 80_000 })
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
  await $.turn.complete(main(usage({ write: 80_000 }, HAIKU)))
  expect(await $.prompt.submit({ text: 'go on' } as never)).toMatchObject({ text: 'go on' })
  expect(seen.toasts).toEqual([])
})

test(
  'the confirm price step gate holds the prompt once and names both rates',
  { options: { priceStepGate: 'confirm' } },
  async ($, on) => {
    const clock = mock.clock(on)
    await clock.set(T0)
    const seen = engine(on)
    await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
    await $.turn.complete(main(usage({ write: 182_000 }, HAIKU)))

    const held = await $.prompt.submit({ text: 'refactor the store' } as never)
    expect(held).toMatchObject({
      drop: expect.stringMatching(
        /^Held: Context is 182K tokens, past the 100K step.*\$0\.50 per MTok input.*up from \$0\.10.*Submit again to send it\.$/,
      ),
    })
    await clock.advance(1)
    await clock.settle()
    expect(seen.fills).toEqual(['refactor the store'])
    expect(await $.prompt.submit({ text: 'refactor the store' } as never)).toMatchObject({ text: 'refactor the store' })
  },
)

test(
  'a model without a price step is never gated on prompt size',
  { options: { priceStepGate: 'confirm' } },
  async ($, on) => {
    const clock = mock.clock(on)
    await clock.set(T0)
    const seen = engine(on, { context: 900_000 })
    await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
    await $.turn.complete(main(usage({ write: 900_000 })))
    expect(await $.prompt.submit({ text: 'hi' } as never)).toMatchObject({ text: 'hi' })
    expect(seen.toasts).toEqual([])
  },
)

test(
  'two confirm gates hold one prompt once, with both reasons',
  { options: { cacheGate: 'confirm', priceStepGate: 'confirm' } },
  async ($, on) => {
    const clock = mock.clock(on)
    await clock.set(T0)
    engine(on)
    await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
    await $.turn.complete(main(usage({ write: 182_000 }, HAIKU)))
    await clock.advance(10 * MIN)
    await clock.settle()

    const held = await $.prompt.submit({ text: 'again' } as never)
    expect(held).toMatchObject({ drop: expect.stringMatching(/Cache expired 5m ago.*past the 100K step/) })
    expect(await $.prompt.submit({ text: 'again' } as never)).toMatchObject({ text: 'again' })
  },
)

test(
  'the budget gate counts the main session and its subagents',
  { options: { sessionBudgetUsd: 1, budgetGate: 'confirm' } },
  async ($, on) => {
    const clock = mock.clock(on)
    await clock.set(T0)
    const seen = engine(on)
    await $.session.start({ cwd: ROOT, surface: 'desktop', isInteractive: true })
    await $.turn.complete(main(usage({ write: 182_000 })))
    expect(await $.prompt.submit({ text: 'under budget' } as never)).toMatchObject({ text: 'under budget' })

    await $.turn.complete({
      answer: 'done',
      durationMs: 1000,
      isAborted: false,
      turnId: 's',
      reason: 'answer',
      agentId: 'ag9',
      usage: usage({ input: 100_000, output: 10_000 }),
    } as never)

    const held = await $.prompt.submit({ text: 'over budget' } as never)
    expect(held).toMatchObject({
      drop: "Held: Session spend $1.52 has passed your $1.00 budget (main $0.92, subagents $0.60 at the mod's rates). Submit again to send it.",
    })
    expect(await $.prompt.submit({ text: 'over budget' } as never)).toMatchObject({ text: 'over budget' })

    await $.command.run(RUN_MAGUS)
    const ui = await $.ui.mount(PANE)
    expect(
      await ui.find({
        type: 'Text',
        text: /Session spend \$1\.52\/\$1\.00 \(main \$0\.92, subagents \$0\.60 at the mod's rates\)/,
      }),
    ).toBeDefined()
  },
)

test('no budget set means the budget gate never speaks', { options: { budgetGate: 'confirm' } }, async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  const seen = engine(on)
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
  await $.turn.complete(main(usage({ output: 400_000 })))
  expect(await $.prompt.submit({ text: 'hi' } as never)).toMatchObject({ text: 'hi' })
  expect(seen.toasts).toEqual([])
})

test('a response on an unpriced model makes the spend a floor', async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  engine(on)
  await $.session.start({ cwd: ROOT, surface: 'desktop', isInteractive: true })
  await $.turn.complete(main(usage({ write: 182_000 }, 'mystery-model')))
  await $.command.run(RUN_MAGUS)
  const ui = await $.ui.mount(PANE)
  expect(await ui.find({ type: 'Text', text: /Session spend \$0\.00\+/ })).toBeDefined()
})

test('Copy fix says when nothing was copied, and names the command to run', async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  const seen = engine(on, { workspace: true, noLocalBinary: true, noClipboard: true })
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
  await clock.advance(1)
  await clock.settle()
  const ui = await $.ui.mount(BAND)
  await ui.press({ key: 'copy-guard-off' })
  expect(seen.toasts.at(-1)).toMatch(/^Not copied \(no-clipboard\)\. Run: GOEXPERIMENT=jsonv2 go run/)
})

test('Copy fix confirms a copy that reached the clipboard', async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  const seen = engine(on, { workspace: true, noLocalBinary: true })
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
  await clock.advance(1)
  await clock.settle()
  const ui = await $.ui.mount(BAND)
  await ui.press({ key: 'copy-guard-off' })
  expect(seen.toasts.at(-1)).toMatch(/^Copied: GOEXPERIMENT=jsonv2 go run/)
})

const HOLDERS = [
  {
    project: '.',
    target: 'go-test',
    pid: 21068,
    memory_mb: 4470,
    slots: 4,
    dir: '/r/magus-integrate',
    command: './magus run go-test .',
    since: new Date(T0 - 3 * MIN).toISOString(),
    owner: 'fix-cache',
  },
  {
    project: '.',
    target: 'go-test',
    pid: 23224,
    memory_mb: 4470,
    slots: 4,
    dir: '/r/magus-stop',
    command: './magus run go-test .',
    since: new Date(T0 - MIN).toISOString(),
    owner: 'unleased',
  },
]
const budgetStatusOf = (capacity: Record<string, unknown>) =>
  JSON.stringify({
    server: { pid: 1 },
    pool: { socket: `unix://${SOCK}`, version: 'v1' },
    mcp_endpoint: { state: 'serving' },
    broker: {
      pid: 51051,
      capacity: { budget_mb: 24064, held_mb: 8940, budget_slots: 10, held_slots: 8, holders: HOLDERS, ...capacity },
    },
  })
const BUDGET_STATUS = budgetStatusOf({
  waiting: [
    {
      pid: 30001,
      project: '.',
      target: 'lint',
      slots: 2,
      dir: '/r/magus-a',
      owner: 'job-b',
      since: new Date(T0).toISOString(),
    },
  ],
  refused: [
    {
      at: new Date(T0).toISOString(),
      pid: 30100,
      project: '.',
      target: 'ci',
      slots: 4,
      dir: '/r/magus-b',
      owner: 'spawned by root',
      fits: true,
      holders: [HOLDERS[0]],
    },
  ],
})
const answerStatusWith = (stdout: string) => (argv: readonly string[]) =>
  argv.join(' ') === `${ROOT}/magus -s server status -o json` ? { exitCode: 0, stdout } : undefined

test('the build budget fills the Budget tab and stays out of the status line and the band', async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  const seen = engine(on, { workspace: true, serverUp: true, run: answerStatusWith(BUDGET_STATUS) })
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
  await clock.advance(1)
  await clock.settle()
  expect(seen.statuses.at(-1)).toBeUndefined()

  const band = await $.ui.mount(BAND)
  expect(await band.find({ type: 'Text', text: /MGS3009/ })).toBeUndefined()
  expect(await band.find({ type: 'Text', text: 'engine' })).toBeDefined()

  const pane = await $.ui.mount(PANE)
  await pane.press({ key: 'tab-budget' })
  await clock.advance(1)
  await clock.settle()
  expect(await pane.find({ type: 'Text', text: /^8\/10 slots, 8\.7\/23\.5 GiB held/ })).toBeDefined()
  expect(
    await pane.find({
      type: 'Text',
      text: /pid 23224 · go-test · unleased · 4 slots · 4\.4 GiB · held 1m · in magus-stop/,
    }),
  ).toBeDefined()
  expect(await pane.find({ type: 'Text', text: 'Waiting for a seat' })).toBeDefined()
  expect(
    await pane.find({ type: 'Text', text: /pid 30001 · lint · job-b · 2 slots · waiting 0s · in magus-a/ }),
  ).toBeDefined()
  expect(await pane.find({ type: 'Text', text: 'Refused on this machine' })).toBeDefined()
  expect(await pane.find({ type: 'Text', text: /held by fix-cache pid 21068/ })).toBeDefined()
})

test('MGS3009 in a magus run output raises no band: the broker JSON is the source', async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  engine(on, { workspace: true, serverUp: true, run: answerStatusWith(budgetStatusOf({})) })
  on('tool.call', () => ({ result: { stdout: 'ok', stderr: '' }, text: '[MGS3009] not starting (root) ci' }) as never)
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
  await $.tool.call({ tool: 'Bash', command: './magus run lint .' } as never)
  await clock.advance(1)
  await clock.settle()
  const band = await $.ui.mount(BAND)
  expect(await band.find({ type: 'Text', text: 'engine' })).toBeDefined()
})

const spawnOf = (over: Record<string, unknown> = {}) =>
  ({
    tool_use_id: 't1',
    prompt: 'look it up',
    description: 'look it up',
    subagentType: 'general-purpose',
    provider: { plugin: 'engine' },
    parentModel: 'claude-opus-5-5',
    background: false,
    fork: false,
    ...over,
  }) as never

// The engine, the mocked clock and a started session, for tests that need nothing else.
async function boot($: Engine, on: On, opts: Opts = {}): Promise<Seen> {
  const clock = mock.clock(on)
  await clock.set(T0)
  const seen = engine(on, opts)
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
  return seen
}

const OVER_OPUS = 'Subagent will run on claude-opus-5-5 at $4.00 per MTok input, over your $2.00 cap.'

describe('the spawn gate', () => {
  test('is off by default: an expensive spawn goes ahead with no toast', async ($, on) => {
    const seen = await boot($, on)
    expect(await $.agent.spawn(spawnOf())).toMatchObject({ agentId: 'ag1' })
    expect(seen.toasts).toEqual([])
  })

  test('an explicit model decides, whatever the parent runs on', { options: { spawnGate: 'deny' } }, async ($, on) => {
    await boot($, on, { env: { CLAUDE_CODE_SUBAGENT_MODEL: 'opus' } })
    expect(await $.agent.spawn(spawnOf({ model: 'haiku' }))).toMatchObject({ agentId: 'ag1' })
    expect(await $.agent.spawn(spawnOf({ model: 'sonnet' }))).toMatchObject({ agentId: 'ag1' })
    expect(await $.agent.spawn(spawnOf({ model: 'claude-fable-5-1' }))).toMatchObject({
      deny: expect.stringMatching(
        /^Subagent will run on claude-fable-5-1 at \$10\.00 per MTok input, over your \$2\.00 cap\./,
      ),
    })
  })

  test(
    'deny refuses the spawn before it starts and names the cheaper route',
    { options: { spawnGate: 'deny' } },
    async ($, on) => {
      const seen = await boot($, on)
      const r = await $.agent.spawn(spawnOf())
      expect(r).toMatchObject({
        deny: expect.stringMatching(/^Subagent will run on claude-opus-5-5.*magus-scout.*model: haiku\.$/),
      })
      expect(r.agentId).toBeUndefined()
      expect(seen.toasts).toEqual([])
      expect(await $.agent.spawn(spawnOf({ model: 'haiku' }))).toMatchObject({ agentId: 'ag1' })
    },
  )

  test(
    'a definition that sets a model decides; one that sets none inherits; the env fills in for it',
    { options: { spawnGate: 'deny' } },
    async ($, on) => {
      await boot($, on, {
        files: {
          [`${ROOT}/.claude/agents/magus-scout.md`]: '---\nname: "magus-scout"\nmodel: haiku\n---\nbody',
          [`${ROOT}/.claude/agents/planner.md`]: '---\nname: planner\n---\nbody',
          '/home/me/.claude/agents/archivist.md': '---\nmodel: claude-fable-5-1\n---\n',
        },
        env: { HOME: '/home/me' },
      })
      expect(await $.agent.spawn(spawnOf({ subagentType: 'magus-scout' }))).toMatchObject({ agentId: 'ag1' })
      expect(await $.agent.spawn(spawnOf({ subagentType: 'planner' }))).toMatchObject({
        deny: expect.stringContaining(OVER_OPUS),
      })
      expect(await $.agent.spawn(spawnOf({ subagentType: 'archivist' }))).toMatchObject({
        deny: expect.stringMatching(/claude-fable-5-1 at \$10\.00/),
      })
    },
  )

  test(
    'CLAUDE_CODE_SUBAGENT_MODEL replaces the parent for a definition with no model',
    { options: { spawnGate: 'deny' } },
    async ($, on) => {
      await boot($, on, { env: { CLAUDE_CODE_SUBAGENT_MODEL: 'haiku' } })
      expect(await $.agent.spawn(spawnOf())).toMatchObject({ agentId: 'ag1' })
    },
  )

  test(
    'a fork runs on the parent model, so a model on the call does not save it',
    { options: { spawnGate: 'deny' } },
    async ($, on) => {
      await boot($, on)
      expect(await $.agent.spawn(spawnOf({ fork: true, model: 'haiku' }))).toMatchObject({
        deny: expect.stringContaining(OVER_OPUS),
      })
    },
  )

  test(
    'a definition the mod cannot read is judged by the model the engine resolved, after the fact',
    { options: { spawnGate: 'deny' } },
    async ($, on) => {
      const seen = await boot($, on, { spawnedModel: 'claude-opus-5-5' })
      await $.agent.spawn(spawnOf({ subagentType: 'Explore' }))
      expect(seen.toasts).toEqual([OVER_OPUS])
    },
  )

  test(
    'an unreadable definition that resolves cheap says nothing',
    { options: { spawnGate: 'deny' } },
    async ($, on) => {
      const seen = await boot($, on)
      await $.agent.spawn(spawnOf({ subagentType: 'Explore' }))
      expect(seen.toasts).toEqual([])
    },
  )

  test('a zero cap is off', { options: { spawnMaxInputUsdPerMTok: 0, spawnGate: 'deny' } }, async ($, on) => {
    const seen = await boot($, on)
    expect(await $.agent.spawn(spawnOf())).toMatchObject({ agentId: 'ag1' })
    expect(seen.toasts).toEqual([])
  })

  test('the gate set to off does nothing', { options: { spawnGate: 'off' } }, async ($, on) => {
    const seen = await boot($, on)
    expect(await $.agent.spawn(spawnOf())).toMatchObject({ agentId: 'ag1' })
    expect(seen.toasts).toEqual([])
  })

  test('a model the rates do not price is left alone', { options: { spawnGate: 'deny' } }, async ($, on) => {
    await boot($, on)
    expect(await $.agent.spawn(spawnOf({ model: 'mystery-model' }))).toMatchObject({ agentId: 'ag1' })
  })

  test('a warn value is gone: it reads as off', { options: { spawnGate: 'warn' } }, async ($, on) => {
    await boot($, on)
    expect(await $.agent.spawn(spawnOf())).toMatchObject({ agentId: 'ag1' })
  })
})
