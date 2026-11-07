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

function engine(on: On) {
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  on('session.root', () => ({ value: '/w' }))
  on('env.get', () => ({ value: HOME }))
  on('fs.exists', ($, e) => ({ value: e.path in FILES }))
  on('fs.read', ($, e) => ({ value: FILES[e.path] ?? '' }))
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
      props: { text: `Wrote \`~/Library/Application Support/Claude/side-session-notes/local_1/inventory.jsonl\`.`, isFirstOfReply: true },
    })
    expect(await ui.find({ type: 'Text', text: 'engine' })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: `${INVENTORY} · 2 rows, 1 line failed to parse` })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /^✖ line 2: / })).toBeDefined()
    const table = await ui.find({ type: 'Markdown' })
    expect(table?.props.text).toBe(
      [
        '| line | title | status | fix_write_set | size |',
        '| --- | --- | --- | --- | --- |',
        '| 1002 | Fork binds lease | UNSURE | ["internal/job/checkout.go"] | M |',
        '| 1020 | Spawn names no row | OPEN | [] | S |',
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
