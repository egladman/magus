import { expect, test } from 'claude-code/testing'
import type { Engine } from 'claude-code/testing'
import type { On } from 'claude-code'

import { headerOf, structuredOf } from '../hooks/json'

// Cut from `magus --dry-run job fork ... -o json`, which exits 1 while a
// problem remains, so the call errors and its text leads with the exit line.
const FORK = JSON.stringify(
  {
    job: {
      api_version: '',
      id: 'demo/json-explorer',
      criteria: 'draw JSON as a tree',
      write_paths: ['docs/guides/integrations/agents/claude-code/mod/hooks/json-view.tsx'],
      model: 'economy',
      check: { target: 'mod-test', project: '.' },
      read_only: false,
    },
    problems: [{ code: 'MGS3040', field: 'check', message: 'names target "mod-test", which project "." does not run' }],
    write_proof: 'alone',
  },
  null,
  2,
)

// `magus refs 'symbol:buzz `std/lib/reclaimdisk.buzz`/magusBin().' -o json`,
// less its definition field.
const REFS = JSON.stringify(
  {
    api_version: 'magus.knowledge/v1alpha1',
    symbol: 'symbol:buzz `std/lib/reclaimdisk.buzz`/magusBin().',
    label: 'magusBin',
    file_count: 1,
    ref_count: 2,
    defs: [{ file: 'std/lib/reclaimdisk.buzz', count: 0, lines: [549] }],
    refs: [{ file: 'std/lib/reclaimdisk.buzz', count: 2, lines: [561, 688] }],
    signature: 'fun magusBin() > str !> any',
    answer: { verdict: 'found' },
  },
  null,
  2,
)

const BASH = (stdout: string) => ({ stdout, stderr: '', interrupted: false })

// engine draws a dim "engine" for every site and records the output each
// ToolUse row was handed.
function engine(on: On) {
  const outputs: unknown[] = []
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  on('ui.render', ($, e) => {
    if (e.component === 'ToolUse') outputs.push(e.props.output)
    const { Text } = $.ui.resolve(e)
    return <Text>engine</Text>
  })
  return { outputs }
}

type Surface = 'terminal' | 'desktop'

// The terminal draws a call's output in its ToolResult block; desktop draws it
// in the ToolUse row, which then carries the tree.
function mount($: Engine, surface: Surface, id: string, output: unknown, isErrored = false) {
  if (surface === 'terminal') {
    return $.ui.mount({
      plugin: 'magus',
      surface,
      component: 'ToolResult',
      requestId: id,
      props: { tool_use_id: id, tool: 'Bash', output, isErrored },
    })
  }
  return $.ui.mount({
    plugin: 'magus',
    surface,
    component: 'ToolUse',
    requestId: id,
    props: {
      tool_use_id: id,
      tool: 'Bash',
      input: { command: 'magus' },
      output,
      isRunning: false,
      isErrored,
      isInterrupted: false,
    },
  })
}

test('structuredOf reads a value between an exit line and trailing prose', () => {
  const s = structuredOf(`Exit code 2\n${REFS}\nmagus refs: no node matches "x"`)
  expect(s?.kind).toBe('json')
  if (s?.kind !== 'json') return
  expect(s.lead).toBe('Exit code 2')
  expect(s.trail).toBe('magus refs: no node matches "x"')
})

test('structuredOf reads JSON Lines and refuses prose and scalars', () => {
  expect(structuredOf('{"a":1}\n{"a":2}\n')).toEqual({ kind: 'jsonl', records: [{ a: 1 }, { a: 2 }] })
  expect(structuredOf('42')).toBeNull()
  expect(structuredOf('ok\nsee below\n{"a": 1}')).toBeNull()
})

test('headerOf names the envelope, skipping an empty api_version', () => {
  const envelope = { api_version: 'magus.job/v1alpha1', kind: 'Job', metadata: { name: 'a/b' }, spec: {} }
  expect(headerOf(envelope)).toBe('magus.job/v1alpha1 · Job · a/b')
  expect(headerOf({ api_version: '', id: 'x' })).toBeNull()
})

for (const surface of ['terminal', 'desktop'] as const) {
  test(`${surface}: a job fork record draws closed, opens on press, and shows raw`, async ($, on) => {
    const seen = engine(on)
    await $.session.start({ cwd: '/w', surface, isInteractive: true })
    const ui = await mount($, surface, 't1', `Exit code 1\n${FORK}`, true)
    if (surface === 'terminal') expect(await ui.find({ type: 'Text', text: 'engine' })).toBeUndefined()
    else expect(seen.outputs).toEqual(['Exit code 1'])
    expect(await ui.find({ type: 'Text', text: 'Exit code 1' })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /\{…\} 3 keys/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /"job"/ })).toBeUndefined()

    await ui.press({ key: 'json' })
    expect(await ui.find({ type: 'Text', text: /"job"/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /"demo\/json-explorer"/ })).toBeUndefined()

    await ui.press({ key: 'json/job' })
    expect(await ui.find({ type: 'Text', text: /"demo\/json-explorer"/ })).toBeDefined()

    await ui.press({ key: 'json-raw' })
    expect((await ui.find({ type: 'Code' }))?.props.source).toBe(FORK)
  })

  test(`${surface}: a refs record names its api_version while closed`, async ($, on) => {
    const seen = engine(on)
    await $.session.start({ cwd: '/w', surface, isInteractive: true })
    const ui = await mount($, surface, 't2', BASH(REFS))
    if (surface === 'desktop') expect(seen.outputs).toEqual([BASH('')])
    expect(await ui.find({ type: 'Text', text: /magus\.knowledge\/v1alpha1/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /\{…\} 9 keys/ })).toBeDefined()
  })

  test(`${surface}: JSON Lines draws one closed row per record`, async ($, on) => {
    engine(on)
    await $.session.start({ cwd: '/w', surface, isInteractive: true })
    const rows = ['{"api_version":"magus.job/v1alpha1","id":"a"}', '{"api_version":"magus.job/v1alpha1","id":"b"}']
    const ui = await mount($, surface, 't3', BASH(rows.join('\n')))
    expect(await ui.find({ type: 'Text', text: 'JSON Lines · 2 records' })).toBeDefined()
    expect(await ui.find({ type: 'Button', key: 'json#0' })).toBeDefined()
    expect(await ui.find({ type: 'Button', key: 'json#1' })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: 'magus.job/v1alpha1 · a' })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: 'magus.job/v1alpha1 · b' })).toBeDefined()
  })

  test(`${surface}: prose output is the engine's own`, async ($, on) => {
    const seen = engine(on)
    await $.session.start({ cwd: '/w', surface, isInteractive: true })
    const ui = await mount($, surface, 't4', BASH('ok'))
    expect(await ui.find({ type: 'Text', text: 'engine' })).toBeDefined()
    expect(await ui.find({ type: 'Button', key: 'json-raw' })).toBeUndefined()
    if (surface === 'desktop') expect(seen.outputs).toEqual([BASH('ok')])
  })
}

test('desktop: the result block is left to the engine, so the tree draws once', async ($, on) => {
  engine(on)
  await $.session.start({ cwd: '/w', surface: 'desktop', isInteractive: true })
  const ui = await $.ui.mount({
    plugin: 'magus',
    surface: 'desktop',
    component: 'ToolResult',
    requestId: 't5',
    props: { tool_use_id: 't5', tool: 'Bash', output: BASH(REFS), isErrored: false },
  })
  expect(await ui.find({ type: 'Text', text: 'engine' })).toBeDefined()
  expect(await ui.find({ type: 'Button', key: 'json-raw' })).toBeUndefined()
})
