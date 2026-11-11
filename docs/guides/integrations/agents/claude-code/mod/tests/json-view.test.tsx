import { expect, test } from 'claude-code/testing'
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

// `magus refs 'symbol:buzz `std/lib/reclaimdisk.buzz`/magusBin().' -o json`, whole.
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

function engine(on: On) {
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  on('ui.render', ($, e) => {
    const { Text } = $.ui.resolve(e)
    return <Text>engine</Text>
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
    engine(on)
    await $.session.start({ cwd: '/w', surface, isInteractive: true })
    const ui = await $.ui.mount({
      plugin: 'magus',
      surface,
      component: 'ToolResult',
      requestId: 't1',
      props: { tool_use_id: 't1', tool: 'Bash', output: `Exit code 1\n${FORK}`, isErrored: true },
    })
    expect(await ui.find({ type: 'Text', text: 'engine' })).toBeUndefined()
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
    engine(on)
    await $.session.start({ cwd: '/w', surface, isInteractive: true })
    const ui = await $.ui.mount({
      plugin: 'magus',
      surface,
      component: 'ToolResult',
      requestId: 't2',
      props: {
        tool_use_id: 't2',
        tool: 'Bash',
        output: { stdout: REFS, stderr: '', interrupted: false },
        isErrored: false,
      },
    })
    expect(await ui.find({ type: 'Text', text: /magus\.knowledge\/v1alpha1/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /\{…\} 9 keys/ })).toBeDefined()
  })

  test(`${surface}: JSON Lines draws one closed row per record`, async ($, on) => {
    engine(on)
    await $.session.start({ cwd: '/w', surface, isInteractive: true })
    const rows = ['{"api_version":"magus.job/v1alpha1","id":"a"}', '{"api_version":"magus.job/v1alpha1","id":"b"}']
    const ui = await $.ui.mount({
      plugin: 'magus',
      surface,
      component: 'ToolResult',
      requestId: 't3',
      props: {
        tool_use_id: 't3',
        tool: 'Bash',
        output: { stdout: rows.join('\n'), stderr: '', interrupted: false },
        isErrored: false,
      },
    })
    expect(await ui.find({ type: 'Text', text: 'JSON Lines · 2 records' })).toBeDefined()
    expect(await ui.find({ type: 'Button', key: 'json#0' })).toBeDefined()
    expect(await ui.find({ type: 'Button', key: 'json#1' })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: 'magus.job/v1alpha1 · a' })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: 'magus.job/v1alpha1 · b' })).toBeDefined()
  })

  test(`${surface}: prose output is the engine's own`, async ($, on) => {
    engine(on)
    await $.session.start({ cwd: '/w', surface, isInteractive: true })
    const ui = await $.ui.mount({
      plugin: 'magus',
      surface,
      component: 'ToolResult',
      requestId: 't4',
      props: {
        tool_use_id: 't4',
        tool: 'Bash',
        output: { stdout: 'ok', stderr: '', interrupted: false },
        isErrored: false,
      },
    })
    expect(await ui.find({ type: 'Text', text: 'engine' })).toBeDefined()
  })
}
