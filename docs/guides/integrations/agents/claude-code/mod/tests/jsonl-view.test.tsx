import { expect, test } from 'claude-code/testing'
import type { On } from 'claude-code'

const HOME = '/Users/e'
const NOTES = `${HOME}/Library/Application Support/Claude/side-session-notes/local_1`
const INVENTORY = `${NOTES}/inventory.jsonl`
const FILES: Record<string, string> = {
  [INVENTORY]: [
    '{"line": 1002, "title": "Fork binds lease", "status": "UNSURE", "fix_write_set": ["internal/job/checkout.go"], "size": "M"}',
    '{"line": 1013, "title": "Re-forking fails",',
    '{"line": 1020, "title": "Spawn names no row", "status": "OPEN", "fix_write_set": [], "size": "S"}',
  ].join('\n'),
}

type Opts = { home?: string | undefined; existsDenied?: boolean; files?: Record<string, string>; reads?: string[] }

function engine(on: On, opts: Opts = {}) {
  const files = { ...FILES, ...opts.files }
  const home = 'home' in opts ? opts.home : HOME
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  on('session.root', () => ({ value: '/w' }))
  on('env.get', () => ({ value: home }))
  on('fs.exists', ($, e) => (opts.existsDenied ? ({ deny: 'permission denied' } as never) : { value: e.path in files }))
  on('fs.stat', ($, e) => ({ value: { kind: 'file', size: files[e.path]?.length ?? 0, mtimeMs: 1 } }) as never)
  on('fs.read', ($, e) => {
    opts.reads?.push(e.path)
    return { value: files[e.path] ?? '' }
  })
  on('ui.render', ($, e) => {
    const { Text } = $.ui.resolve(e)
    return <Text>engine</Text>
  })
}

for (const surface of ['terminal', 'desktop'] as const) {
  test(`${surface}: a reply naming a .jsonl file draws its rows and a red line per bad line`, async ($, on) => {
    engine(on)
    await $.session.start({ cwd: '/w', surface, isInteractive: true })
    const ui = await $.ui.mount({
      plugin: 'magus',
      surface,
      component: 'AssistantMessage',
      requestId: 'm1',
      props: {
        text: `Wrote \`~/Library/Application Support/Claude/side-session-notes/local_1/inventory.jsonl\`.`,
        isFirstOfReply: true,
      },
    })
    expect(await ui.find({ type: 'Text', text: 'engine' })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: `${INVENTORY} · 2 rows, 1 line failed to parse` })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /^✖ line 2: / })).toBeDefined()
    const table = await ui.find({ type: 'Markdown' })
    expect(table?.props.text).toBe(
      [
        '| line | title | status | fix\\_write\\_set | size |',
        '| --- | --- | --- | --- | --- |',
        '| 1002 | Fork binds lease | UNSURE | \\["internal/job/checkout.go"\\] | M |',
        '| 1020 | Spawn names no row | OPEN | \\[\\] | S |',
      ].join('\n'),
    )
  })

  test(`${surface}: a mention of a file that is not there draws one dim line`, async ($, on) => {
    engine(on)
    await $.session.start({ cwd: '/w', surface, isInteractive: true })
    const ui = await $.ui.mount({
      plugin: 'magus',
      surface,
      component: 'AssistantMessage',
      requestId: 'm2',
      props: { text: 'See `out/runs.jsonl`.', isFirstOfReply: true },
    })
    expect(await ui.find({ type: 'Text', text: '○ /w/out/runs.jsonl: no such file' })).toBeDefined()
    expect(await ui.find({ type: 'Markdown' })).toBeUndefined()
  })

  test(`${surface}: SendUserFile of a .jsonl file draws the table under the call`, async ($, on) => {
    engine(on)
    await $.session.start({ cwd: '/w', surface, isInteractive: true })
    const ui = await $.ui.mount({
      plugin: 'magus',
      surface,
      component: 'ToolUse',
      requestId: 't1',
      props: {
        tool_use_id: 't1',
        tool: 'SendUserFile',
        input: { files: [INVENTORY, `${NOTES}/notes.md`], status: 'normal' },
        isRunning: false,
        isErrored: false,
        isInterrupted: false,
      },
    })
    expect(await ui.find({ type: 'Text', text: `${INVENTORY} · 2 rows, 1 line failed to parse` })).toBeDefined()
    expect(await ui.find({ type: 'Markdown' })).toBeDefined()
  })

  test(`${surface}: a reply that names no .jsonl file is the engine's alone`, async ($, on) => {
    engine(on)
    await $.session.start({ cwd: '/w', surface, isInteractive: true })
    const ui = await $.ui.mount({
      plugin: 'magus',
      surface,
      component: 'AssistantMessage',
      requestId: 'm3',
      props: { text: 'Wrote report.json.', isFirstOfReply: true },
    })
    expect(await ui.find({ type: 'Text', text: 'engine' })).toBeDefined()
    expect(await ui.find({ type: 'Markdown' })).toBeUndefined()
  })
}

const reply = (surface: 'terminal' | 'desktop', requestId: string, text: string) => ({
  plugin: 'magus',
  surface,
  component: 'AssistantMessage' as const,
  requestId,
  props: { text, isFirstOfReply: true },
})

for (const surface of ['terminal', 'desktop'] as const) {
  test(`${surface}: a file over 2 MB draws a red line naming its size and no table`, async ($, on) => {
    engine(on, { files: { '/w/big.jsonl': 'x'.repeat(2_000_001) } })
    await $.session.start({ cwd: '/w', surface, isInteractive: true })
    const ui = await $.ui.mount(reply(surface, 'b1', 'See `big.jsonl`.'))
    expect(await ui.find({ type: 'Text', text: /^✖ \/w\/big\.jsonl: 2000001 bytes/ })).toBeDefined()
    expect(await ui.find({ type: 'Markdown' })).toBeUndefined()
  })

  test(`${surface}: HOME unset draws a red line for a ~ path and keeps the engine row`, async ($, on) => {
    engine(on, { home: undefined })
    await $.session.start({ cwd: '/w', surface, isInteractive: true })
    const ui = await $.ui.mount(reply(surface, 'h1', 'See ~/runs.jsonl.'))
    expect(await ui.find({ type: 'Text', text: '✖ ~/runs.jsonl: HOME is not set' })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: 'engine' })).toBeDefined()
  })

  test(`${surface}: a rejected exists draws a red line and keeps the engine row`, async ($, on) => {
    engine(on, { existsDenied: true })
    await $.session.start({ cwd: '/w', surface, isInteractive: true })
    const ui = await $.ui.mount(reply(surface, 'x1', 'See `a.jsonl`.'))
    expect(await ui.find({ type: 'Text', text: /^✖ \/w\/a\.jsonl: / })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: 'engine' })).toBeDefined()
  })

  test(`${surface}: past five parse errors the rest are counted`, async ($, on) => {
    const bad = Array.from({ length: 8 }, () => '{').join('\n')
    engine(on, { files: { '/w/bad.jsonl': bad } })
    await $.session.start({ cwd: '/w', surface, isInteractive: true })
    const ui = await $.ui.mount(reply(surface, 'e1', 'See `bad.jsonl`.'))
    expect(await ui.find({ type: 'Text', text: '/w/bad.jsonl · 0 rows, 8 lines failed to parse' })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: '✖ 3 more lines failed to parse' })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /^✖ line 6: / })).toBeUndefined()
  })

  test(`${surface}: two spellings of one file draw once, and a redraw does not read it again`, async ($, on) => {
    const reads: string[] = []
    engine(on, { files: { [`/w/${surface}-a.jsonl`]: '{"a": 1}' }, reads })
    await $.session.start({ cwd: '/w', surface, isInteractive: true })
    const text = `See \`${surface}-a.jsonl\` and \`./x/../${surface}-a.jsonl\`.`
    const ui = await $.ui.mount(reply(surface, 'd1', text))
    expect(await ui.find({ type: 'Text', text: `/w/${surface}-a.jsonl · 1 row` })).toBeDefined()
    await $.ui.mount(reply(surface, 'd2', text))
    expect(reads).toEqual([`/w/${surface}-a.jsonl`])
  })
}
