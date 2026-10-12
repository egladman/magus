import { describe, expect, mock, test } from 'claude-code/testing'
import type { Engine } from 'claude-code/testing'
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
  env?: Record<string, string>
  // The prompt tokens the session reports; 182K unless a test needs another.
  context?: number
  // The model the engine resolves a spawn to; haiku unless a test needs another.
  spawnedModel?: string
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
  on('session.usage', () => ({ value: { startedAt: 0, context: { tokens: opts.context ?? 182_000, window: 1_000_000 }, rateLimits: [] } as never }))
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
  expect(seen.statuses.at(-1)).toBe('cache 5m · $0.92')
  await clock.advance(3 * MIN)
  await clock.settle()
  expect(seen.statuses.at(-1)).toBe('cache 2m · $0.92')
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
  expect(seen.statuses.at(-1)).toBe('cache expired · $0.92')

  await $.turn.complete(main(usage({ read: 182_000, write: 300 })))
  expect(seen.statuses.at(-1)).toBe('cache 1h0m · $0.97')
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
  expect(seen.statuses.at(-1)).toBe('⚠ server down · cache 5m · $0.92')
  const ui = await $.ui.mount(BAND)
  expect(await ui.find({ type: 'Text', text: /No magus server/ })).toBeDefined()
  await ui.press({ key: 'hide' })
  expect(await ui.find({ type: 'Text', text: /No magus server/ })).toBeUndefined()
})

const HAIKU = 'claude-haiku-5-5'

test('the price step gate warns past the step by default and still sends', async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  const seen = engine(on)
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
  await $.turn.complete(main(usage({ write: 182_000 }, HAIKU)))

  expect(await $.prompt.submit({ text: 'go on' } as never)).toMatchObject({ text: 'go on' })
  expect(seen.toasts).toEqual([
    'Context is 182K tokens, past the 100K step: every request on claude-haiku-5-5 now pays $0.50 per MTok input and $2.50 output, up from $0.10 and $0.50. Consider /compact.',
  ])
})

test('the price step gate speaks inside its margin', async ($, on) => {
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

test('the confirm price step gate holds the prompt once and names both rates', { options: { priceStepGate: 'confirm' } }, async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  const seen = engine(on)
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
  await $.turn.complete(main(usage({ write: 182_000 }, HAIKU)))

  const held = await $.prompt.submit({ text: 'refactor the store' } as never)
  expect(held).toMatchObject({
    drop: expect.stringMatching(/^Held: Context is 182K tokens, past the 100K step.*\$0\.50 per MTok input.*up from \$0\.10.*Submit again to send it\.$/),
  })
  await clock.advance(1)
  await clock.settle()
  expect(seen.fills).toEqual(['refactor the store'])
  expect(await $.prompt.submit({ text: 'refactor the store' } as never)).toMatchObject({ text: 'refactor the store' })
})

test('a model without a price step is never gated on prompt size', { options: { priceStepGate: 'confirm' } }, async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  const seen = engine(on, { context: 900_000 })
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
  await $.turn.complete(main(usage({ write: 900_000 })))
  expect(await $.prompt.submit({ text: 'hi' } as never)).toMatchObject({ text: 'hi' })
  expect(seen.toasts).toEqual([])
})

test('two confirm gates hold one prompt once, with both reasons', { options: { cacheGate: 'confirm', priceStepGate: 'confirm' } }, async ($, on) => {
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
})

test('the budget gate counts the main session and its subagents', { options: { sessionBudgetUsd: 1, budgetGate: 'confirm' } }, async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(T0)
  const seen = engine(on)
  await $.session.start({ cwd: ROOT, surface: 'desktop', isInteractive: true })
  await $.turn.complete(main(usage({ write: 182_000 })))
  expect(seen.statuses.at(-1)).toBe('cache 5m · $0.92/$1.00')
  expect(await $.prompt.submit({ text: 'under budget' } as never)).toMatchObject({ text: 'under budget' })

  await $.turn.complete(
    ({ answer: 'done', durationMs: 1000, isAborted: false, turnId: 's', reason: 'answer', agentId: 'ag9', usage: usage({ input: 100_000, output: 10_000 }) }) as never,
  )
  expect(seen.statuses.at(-1)).toBe('cache 5m · $1.52/$1.00')

  const held = await $.prompt.submit({ text: 'over budget' } as never)
  expect(held).toMatchObject({ drop: 'Held: Session spend $1.52 has passed your $1.00 budget (main $0.92, subagents $0.60). Submit again to send it.' })
  expect(await $.prompt.submit({ text: 'over budget' } as never)).toMatchObject({ text: 'over budget' })

  await $.command.run(RUN_MAGUS)
  const ui = await $.ui.mount(PANE)
  expect(await ui.find({ type: 'Text', text: /Session spend \$1\.52\/\$1\.00 \(main \$0\.92, subagents \$0\.60\)/ })).toBeDefined()
})

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
  const seen = engine(on)
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true })
  await $.turn.complete(main(usage({ write: 182_000 }, 'mystery-model')))
  expect(seen.statuses.at(-1)).toBe('cache 5m · $0.00+')
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

const OVER_OPUS ='Subagent will run on claude-opus-5-5 at $4.00 per MTok input, over your $2.00 cap.'

describe('the spawn gate', () => {
  test('warns by default when the model costs more than the cap, and the spawn goes ahead', async ($, on) => {
    const seen = await boot($, on)
    expect(await $.agent.spawn(spawnOf())).toMatchObject({ agentId: 'ag1' })
    expect(seen.toasts).toEqual([OVER_OPUS])
  })

  test('an explicit model decides, whatever the parent runs on', async ($, on) => {
    const seen = await boot($, on, { env: { CLAUDE_CODE_SUBAGENT_MODEL: 'opus' } })
    await $.agent.spawn(spawnOf({ model: 'haiku' }))
    await $.agent.spawn(spawnOf({ model: 'sonnet' }))
    expect(seen.toasts).toEqual([])
    await $.agent.spawn(spawnOf({ model: 'claude-fable-5-1' }))
    expect(seen.toasts).toEqual(['Subagent will run on claude-fable-5-1 at $10.00 per MTok input, over your $2.00 cap.'])
  })

  test('deny refuses the spawn before it starts and names the cheaper route', { options: { spawnGate: 'deny' } }, async ($, on) => {
    const seen = await boot($, on)
    const r = await $.agent.spawn(spawnOf())
    expect(r).toMatchObject({ deny: expect.stringMatching(/^Subagent will run on claude-opus-5-5.*magus-scout.*model: haiku\.$/) })
    expect(r.agentId).toBeUndefined()
    expect(seen.toasts).toEqual([])
    expect(await $.agent.spawn(spawnOf({ model: 'haiku' }))).toMatchObject({ agentId: 'ag1' })
  })

  test('a definition that sets a model decides; one that sets none inherits; the env fills in for it', async ($, on) => {
    const seen = await boot($, on, {
      files: {
        [`${ROOT}/.claude/agents/magus-scout.md`]: '---\nname: "magus-scout"\nmodel: haiku\n---\nbody',
        [`${ROOT}/.claude/agents/planner.md`]: '---\nname: planner\n---\nbody',
        '/home/me/.claude/agents/archivist.md': '---\nmodel: claude-fable-5-1\n---\n',
      },
      env: { HOME: '/home/me' },
    })
    await $.agent.spawn(spawnOf({ subagentType: 'magus-scout' }))
    expect(seen.toasts).toEqual([])
    await $.agent.spawn(spawnOf({ subagentType: 'planner' }))
    expect(seen.toasts).toEqual([OVER_OPUS])
    await $.agent.spawn(spawnOf({ subagentType: 'archivist' }))
    expect(seen.toasts.at(-1)).toMatch(/claude-fable-5-1 at \$10\.00/)
  })

  test('CLAUDE_CODE_SUBAGENT_MODEL replaces the parent for a definition with no model', async ($, on) => {
    const seen = await boot($, on, { env: { CLAUDE_CODE_SUBAGENT_MODEL: 'haiku' } })
    await $.agent.spawn(spawnOf())
    expect(seen.toasts).toEqual([])
  })

  test('a fork runs on the parent model, so a model on the call does not save it', async ($, on) => {
    const seen = await boot($, on)
    await $.agent.spawn(spawnOf({ fork: true, model: 'haiku' }))
    expect(seen.toasts).toEqual([OVER_OPUS])
  })

  test('a definition the mod cannot read is judged by the model the engine resolved, after the fact', async ($, on) => {
    const seen = await boot($, on, { spawnedModel: 'claude-opus-5-5' })
    await $.agent.spawn(spawnOf({ subagentType: 'Explore' }))
    expect(seen.toasts).toEqual([OVER_OPUS])
  })

  test('an unreadable definition that resolves cheap says nothing', async ($, on) => {
    const seen = await boot($, on)
    await $.agent.spawn(spawnOf({ subagentType: 'Explore' }))
    expect(seen.toasts).toEqual([])
  })

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

  test('a model the rates do not price is left alone', async ($, on) => {
    const seen = await boot($, on)
    await $.agent.spawn(spawnOf({ model: 'mystery-model' }))
    expect(seen.toasts).toEqual([])
  })
})
