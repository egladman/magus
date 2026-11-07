import { expect, test } from 'claude-code/testing'
import type { On } from 'claude-code'

import { REF_DOC } from '../hooks/refs'

const ROOT = '/w'
const REF = 'out41faa93d10d7'

type Run = { argv: string[] }

function engine(on: On, runs: Run[], answer: { exitCode: number; stdout: string; stderr: string }) {
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  on('session.root', () => ({ value: ROOT }))
  on('fs.exists', ($, e) => ({ value: e.path === `${ROOT}/magus` }))
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('process.run', ($, e) => {
    runs.push({ argv: [...e.argv] })
    return { value: { ...answer, isStdoutTruncated: false, isStderrTruncated: false } }
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
    props: { title: REF, isFocused: true, bodyColumns: 100, placement: 'dock', scroll: { offset: 0, bodyRows: 30 }, view: {} },
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
        output: { stdout: `[fail] lint\n  output: ${REF}\n  details: ./magus query output grd26b6cf2f8904ccf5`, stderr: '' },
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
