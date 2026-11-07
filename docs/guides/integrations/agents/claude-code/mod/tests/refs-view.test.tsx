import { expect, test } from 'claude-code/testing'
import type { On } from 'claude-code'

import { REF_DOC } from '../hooks/refs'

const ROOT = '/w'
const REF = 'out41faa93d10d7'

type Run = { argv: string[] }
type Answer = { exitCode: number; stdout: string; stderr: string }
type Opts = { hasLocalBinary?: boolean; answer?: (argv: readonly string[]) => Promise<Answer> | Answer }

function engine(on: On, runs: Run[], answer: Answer, opts: Opts = {}) {
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  on('session.root', () => ({ value: ROOT }))
  on('fs.exists', ($, e) => ({ value: (opts.hasLocalBinary ?? true) && e.path === `${ROOT}/magus` }))
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('process.run', async ($, e) => {
    runs.push({ argv: [...e.argv] })
    const a = opts.answer ? await opts.answer(e.argv) : answer
    return { value: { ...a, isStdoutTruncated: false, isStderrTruncated: false } }
  })
  on('ui.render', ($, e) => {
    const { Text } = $.ui.resolve(e)
    return <Text>engine</Text>
  })
}

const PANE = (surface: 'terminal' | 'desktop') =>
  ({
    plugin: 'magus',
    surface,
    component: 'Pane',
    requestId: 'magus-ref',
    props: {
      title: REF,
      isFocused: true,
      bodyColumns: 100,
      placement: 'dock',
      scroll: { offset: 0, bodyRows: 30 },
      view: {},
    },
  }) as const

for (const surface of ['terminal', 'desktop'] as const) {
  test(`${surface}: a ref in a reply draws as a link; a press opens its output in the ref pane`, async ($, on) => {
    const runs: Run[] = []
    engine(on, runs, { exitCode: 0, stdout: '\x1b[32m(pass)\x1b[0m parseJsonl\n', stderr: '' })
    await $.session.start({ cwd: ROOT, surface, isInteractive: true })
    const ui = await $.ui.mount({
      plugin: 'magus',
      surface,
      component: 'AssistantMessage',
      requestId: 'm1',
      props: { text: `The run failed; see ${REF}.`, isFirstOfReply: true },
    })
    expect(await ui.find({ type: 'Text', text: 'engine' })).toBeDefined()
    const links = await ui.find({ type: 'Markdown', key: 'refs-m1' })
    expect(links?.props.text).toBe(`↳ [${REF}](${REF_DOC}#${REF} "./magus query output ${REF}")`)

    await ui.press({ key: 'refs-m1', link: { href: `${REF_DOC}#${REF}` } })

    expect(runs).toEqual([{ argv: [`${ROOT}/magus`, 'query', 'output', REF] }])
    const pane = await $.ui.mount(PANE(surface))
    expect(await pane.find({ type: 'Text', text: `./magus query output ${REF}` })).toBeDefined()
    expect((await pane.find({ type: 'Code' }))?.props.source).toBe('(pass) parseJsonl\n')
  })

  test(`${surface}: a ref that magus cannot print shows its error in red`, async ($, on) => {
    const runs: Run[] = []
    engine(on, runs, { exitCode: 1, stdout: '', stderr: 'magus query output: no output stored for out41faa93d10d7\n' })
    await $.session.start({ cwd: ROOT, surface, isInteractive: true })
    const ui = await $.ui.mount({
      plugin: 'magus',
      surface,
      component: 'AssistantMessage',
      requestId: 'm2',
      props: { text: REF, isFirstOfReply: true },
    })
    await ui.press({ key: 'refs-m2', link: { href: `${REF_DOC}#${REF}` } })
    const pane = await $.ui.mount(PANE(surface))
    expect(await pane.find({ type: 'Text', text: `✖ magus query output: no output stored for ${REF}` })).toBeDefined()
    expect(await pane.find({ type: 'Code' })).toBeUndefined()
  })

  test(`${surface}: a finished magus Bash call lists the refs its output names`, async ($, on) => {
    engine(on, [], { exitCode: 0, stdout: '', stderr: '' })
    await $.session.start({ cwd: ROOT, surface, isInteractive: true })
    const ui = await $.ui.mount({
      plugin: 'magus',
      surface,
      component: 'ToolUse',
      requestId: 't1',
      props: {
        tool_use_id: 't1',
        tool: 'Bash',
        input: { command: './magus run lint .' },
        isRunning: false,
        isErrored: true,
        isInterrupted: false,
        output: {
          stdout: `[fail] lint\n  output: ${REF}\n  details: ./magus query output grd26b6cf2f8904ccf5`,
          stderr: '',
        },
      },
    })
    const links = await ui.find({ type: 'Markdown', key: 'refs-t1' })
    expect(links?.props.text).toContain(`[${REF}](`)
    expect(links?.props.text).toContain('[grd26b6cf2f8904ccf5](')
  })

  test(`${surface}: a Bash call that is not magus draws no ref line`, async ($, on) => {
    engine(on, [], { exitCode: 0, stdout: '', stderr: '' })
    await $.session.start({ cwd: ROOT, surface, isInteractive: true })
    const ui = await $.ui.mount({
      plugin: 'magus',
      surface,
      component: 'ToolUse',
      requestId: 't2',
      props: {
        tool_use_id: 't2',
        tool: 'Bash',
        input: { command: 'git log -1' },
        isRunning: false,
        isErrored: false,
        isInterrupted: false,
        output: { stdout: `commit ${REF}`, stderr: '' },
      },
    })
    expect(await ui.find({ type: 'Markdown', key: 'refs-t2' })).toBeUndefined()
  })
}

const REF2 = 'out0123456789ab'
const OK: Answer = { exitCode: 0, stdout: 'ok\n', stderr: '' }

const reply = (surface: 'terminal' | 'desktop', requestId: string, text: string) => ({
  plugin: 'magus',
  surface,
  component: 'AssistantMessage' as const,
  requestId,
  props: { text, isFirstOfReply: true },
})

const link = (ref: string) => ({ href: `${REF_DOC}#${ref}` })

for (const surface of ['terminal', 'desktop'] as const) {
  test(`${surface}: a ref that starts a line of a magus call's output is listed`, async ($, on) => {
    engine(on, [], OK)
    await $.session.start({ cwd: ROOT, surface, isInteractive: true })
    const ui = await $.ui.mount({
      plugin: 'magus',
      surface,
      component: 'ToolUse',
      requestId: 't3',
      props: {
        tool_use_id: 't3',
        tool: 'Bash',
        input: { command: 'cd /w && ./magus run lint .' },
        isRunning: false,
        isErrored: false,
        isInterrupted: false,
        output: { stdout: `${REF}\n${REF2} is next`, stderr: '' },
      },
    })
    const links = await ui.find({ type: 'Markdown', key: 'refs-t3' })
    expect(links?.props.text).toContain(`[${REF}](`)
    expect(links?.props.text).toContain(`[${REF2}](`)
  })

  test(`${surface}: a magus MCP tool call lists the refs its output names`, async ($, on) => {
    engine(on, [], OK)
    await $.session.start({ cwd: ROOT, surface, isInteractive: true })
    const ui = await $.ui.mount({
      plugin: 'magus',
      surface,
      component: 'ToolUse',
      requestId: 't4',
      props: {
        tool_use_id: 't4',
        tool: 'mcp__magus__run',
        input: {},
        isRunning: false,
        isErrored: false,
        isInterrupted: false,
        output: [{ type: 'text', text: `done\n${REF}` }],
      },
    })
    expect(await ui.find({ type: 'Markdown', key: 'refs-t4' })).toBeDefined()
  })

  test(`${surface}: a grd detail prints with query output`, async ($, on) => {
    const runs: Run[] = []
    engine(on, runs, OK)
    await $.session.start({ cwd: ROOT, surface, isInteractive: true })
    const ui = await $.ui.mount(reply(surface, 'g1', 'see grd26b6cf2f8904ccf5'))
    await ui.press({ key: 'refs-g1', link: link('grd26b6cf2f8904ccf5') })
    expect(runs).toEqual([{ argv: [`${ROOT}/magus`, 'query', 'output', 'grd26b6cf2f8904ccf5'] }])
  })

  test(`${surface}: with no ./magus the pane says it ran magus from PATH and shows that command`, async ($, on) => {
    const runs: Run[] = []
    engine(on, runs, OK, { hasLocalBinary: false })
    await $.session.start({ cwd: ROOT, surface, isInteractive: true })
    const ui = await $.ui.mount(reply(surface, 'f1', REF))
    await ui.press({ key: 'refs-f1', link: link(REF) })
    expect(runs).toEqual([{ argv: ['magus', 'query', 'output', REF] }])
    const pane = await $.ui.mount(PANE(surface))
    expect(await pane.find({ type: 'Text', text: `magus query output ${REF}` })).toBeDefined()
    expect(await pane.find({ type: 'Text', text: `no ./magus at ${ROOT}; ran magus from PATH` })).toBeDefined()
  })

  test(`${surface}: output past the cap is cut and says how much was left out`, async ($, on) => {
    engine(on, [], { exitCode: 0, stdout: 'x'.repeat(60_000), stderr: '' })
    await $.session.start({ cwd: ROOT, surface, isInteractive: true })
    const ui = await $.ui.mount(reply(surface, 'c1', REF))
    await ui.press({ key: 'refs-c1', link: link(REF) })
    const pane = await $.ui.mount(PANE(surface))
    expect((await pane.find({ type: 'Code' }))?.props.source).toHaveLength(50_000)
    expect(await pane.find({ type: 'Text', text: '10000 more characters not shown.' })).toBeDefined()
  })

  test(`${surface}: escape sequences other than colors never reach the pane`, async ($, on) => {
    engine(on, [], { exitCode: 0, stdout: '\x1b[2Kline\x1b]0;title\x07\n', stderr: '' })
    await $.session.start({ cwd: ROOT, surface, isInteractive: true })
    const ui = await $.ui.mount(reply(surface, 's1', REF))
    await ui.press({ key: 'refs-s1', link: link(REF) })
    const pane = await $.ui.mount(PANE(surface))
    expect((await pane.find({ type: 'Code' }))?.props.source).toBe('line\n')
  })

  test(`${surface}: a slow read of an earlier ref never overwrites a newer press`, async ($, on) => {
    let release = () => {}
    const gate = new Promise<void>(resolve => {
      release = resolve
    })
    engine(on, [], OK, {
      answer: async argv => {
        if (argv.at(-1) === REF) await gate
        return { exitCode: 0, stdout: `printed ${argv.at(-1)}\n`, stderr: '' }
      },
    })
    await $.session.start({ cwd: ROOT, surface, isInteractive: true })
    const ui = await $.ui.mount(reply(surface, 'r1', `${REF} ${REF2}`))
    await ui.press({ key: 'refs-r1', link: link(REF) })
    await ui.press({ key: 'refs-r1', link: link(REF2) })
    release()
    for (let i = 0; i < 20; i++) await Promise.resolve()
    const pane = await $.ui.mount(PANE(surface))
    expect((await pane.find({ type: 'Code' }))?.props.source).toBe(`printed ${REF2}\n`)
  })
}
