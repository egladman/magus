import { expect, mock, test } from 'claude-code/testing'
import type { On } from 'claude-code'

const ROOT = '/w'
const SOCK = '/c/magus/run/server.sock'
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
const RUN_JOBS = {
  command: 'magus',
  args: '',
  origin: { kind: 'composer' },
  presentation: { isFullscreen: false, columns: 100 },
} as const

const LIST_JOBS = {
  jobs: [
    { id: 'css-reorg', state: 'running', model: 'sonnet', criteria: 'one stylesheet per app', updated: '1000000' },
    { id: 'css-reorg/tokens', parent: 'css-reorg', state: 'declared', updated: '1000000' },
    { id: 'harness', state: 'pass', updated: '10' },
  ],
  stale: ['css-reorg/tokens'],
}

type Call = { url: string; socketPath?: string; body: unknown }

function engine(on: On, opts: { serverUp: boolean; calls: Call[] }) {
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  on('session.root', () => ({ value: ROOT }))
  on('command.register', ($, e) => ({ value: { command: e.name } }))
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('ui.panes', () => ({ value: [{ id: 'magus', title: 'magus', isFocused: true }] as never }))
  on('fs.exists', () => ({ value: true }))
  on('process.run', () => ({
    value: {
      exitCode: opts.serverUp ? 0 : 1,
      stdout: JSON.stringify(opts.serverUp ? { server: { pid: 1 }, pool: { socket: `unix://${SOCK}` } } : { pool_error: 'no such file' }),
      stderr: '',
      isStdoutTruncated: false,
      isStderrTruncated: false,
    },
  }))
  on('http.fetch', ($, e) => {
    opts.calls.push({ url: e.url, socketPath: e.init?.socketPath, body: JSON.parse(e.init?.body ?? 'null') })
    const answers: Record<string, unknown> = {
      'http://magus/magus.job.v1alpha1.JobService/ListJobs': LIST_JOBS,
      'http://magus/magus.activity.v1alpha1.ActivityService/ListActivityEvents': {
        events: [{ time: '2026-10-09T23:56:00Z', kind: 'KIND_FILE_CHANGE', action: 'write', preview: 'console/app.css' }],
      },
      'http://magus/magus.viewer.v1alpha1.ViewerService/ListInvocations': {
        invocations: [{ id: 'inv1', command: { arguments: ['affected', 'ci'] }, startTime: '2026-10-09T23:56:00Z' }],
      },
    }
    return { value: { status: 200, ok: true, headers: {}, text: JSON.stringify(answers[e.url] ?? {}) } }
  })
  on('ui.render', ($, e) => {
    const { Text } = $.ui.resolve(e)
    return <Text>engine</Text>
  })
}

test('the pane reads ListJobs over the server socket, draws the live tree, and watches a job through ActivityService', async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(1_000_060_000)
  const calls: Call[] = []
  engine(on, { serverUp: true, calls })

  await $.session.start({ cwd: ROOT, surface: 'desktop', isInteractive: true })
  await $.command.run(RUN_JOBS)
  await clock.advance(1)
  await clock.settle()

  expect(calls).toEqual([{ url: 'http://magus/magus.job.v1alpha1.JobService/ListJobs', socketPath: SOCK, body: {} }])

  const ui = await $.ui.mount(PANE)
  expect(await ui.find({ type: 'Button', key: 'sel-css-reorg' })).toBeDefined()
  expect(await ui.find({ type: 'Button', text: 'tokens' })).toBeUndefined()
  expect(await ui.find({ type: 'Button', key: 'sel-harness' })).toBeUndefined()
  expect(await ui.find({ type: 'Text', text: /1 stale hidden/ })).toBeDefined()

  await ui.press({ key: 'filter-all' })
  expect(await ui.find({ type: 'Button', text: 'tokens' })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: 'stale' })).toBeDefined()
  await ui.press({ key: 'filter-live' })

  await ui.press({ key: 'sel-css-reorg' })
  expect(await ui.find({ type: 'Text', text: 'one stylesheet per app' })).toBeDefined()
  await ui.press({ key: 'watch' })
  await clock.advance(1)
  await clock.settle()
  expect(calls.at(-1)).toEqual({
    url: 'http://magus/magus.activity.v1alpha1.ActivityService/ListActivityEvents',
    socketPath: SOCK,
    body: { pageSize: 40, filter: { units: ['css-reorg'] } },
  })
  expect(await ui.find({ type: 'Text', text: /file_change write console\/app\.css/ })).toBeDefined()

  await ui.press({ key: 'tab-runs' })
  await clock.advance(1)
  await clock.settle()
  expect(await ui.find({ type: 'Text', text: 'magus affected ci' })).toBeDefined()
})

test('with no server running the pane says how to start one and calls nothing', async ($, on) => {
  const clock = mock.clock(on)
  const calls: Call[] = []
  engine(on, { serverUp: false, calls })

  await $.session.start({ cwd: ROOT, surface: 'desktop', isInteractive: true })
  await $.command.run(RUN_JOBS)
  await clock.advance(1)
  await clock.settle()

  expect(calls).toEqual([])
  const ui = await $.ui.mount(PANE)
  expect(await ui.find({ type: 'Text', text: /magus server start/ })).toBeDefined()
})

test('the Agents tab lists spawned subagents and links one to the job its description names', async ($, on) => {
  const clock = mock.clock(on)
  await clock.set(1_000_060_000)
  const calls: Call[] = []
  engine(on, { serverUp: true, calls })
  on('agent.spawn', () => ({ model: 'claude-haiku-5-5', agentId: 'ag1' }))
  on('turn.complete', () => ({ text: 'done' }) as never)
  on('session.usage', () => ({ value: { startedAt: 0, context: { tokens: 1000, window: 1_000_000 }, rateLimits: [] } as never }))
  on('session.model', () => ({ value: 'claude-opus-5-5' }))

  await $.session.start({ cwd: ROOT, surface: 'desktop', isInteractive: true })
  await $.agent.spawn({
    tool_use_id: 't1',
    prompt: 'restyle',
    description: 'css-reorg/feat tokens',
    subagentType: 'general-purpose',
    provider: { plugin: 'engine' },
    parentModel: 'claude-opus-5-5',
    background: true,
  } as never)

  const ui = await $.ui.mount(PANE)
  await ui.press({ key: 'tab-agents' })
  await clock.advance(1)
  await clock.settle()
  expect(await ui.find({ type: 'Text', text: 'css-reorg/feat tokens' })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: '●' })).toBeDefined()

  await $.turn.complete({ answer: 'done', durationMs: 5, isAborted: false, turnId: 'x', agentId: 'ag1', reason: 'answer' } as never)
  expect(await ui.find({ type: 'Text', text: '✓' })).toBeDefined()

  await ui.press({ key: 'agent-job-ag1' })
  await clock.advance(1)
  await clock.settle()
  expect(await ui.find({ type: 'Text', text: /tokens/ })).toBeDefined()
  expect(await ui.find({ type: 'Button', key: 'watch' })).toBeDefined()
})
