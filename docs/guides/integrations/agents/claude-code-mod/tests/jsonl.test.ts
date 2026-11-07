import { describe, expect, test } from 'claude-code/testing'

import { compact, jsonlPaths, parseJsonl, resolvePath, toMarkdown } from '../hooks/jsonl'

const INVENTORY = [
  '{"line": 1002, "title": "Fork binds lease", "status": "UNSURE", "evidence": "a | b", "fix_write_set": ["internal/job/checkout.go"], "size": "M"}',
  '{"line": 1013, "title": "Re-forking fails", "status": "FIXED", "evidence": "x", "fix_write_set": [], "size": "S"}',
  '',
].join('\n')

describe('parseJsonl', () => {
  test('one column per key in first-seen order, rows in file order', async () => {
    const t = parseJsonl(INVENTORY)
    expect(t.columns).toEqual(['line', 'title', 'status', 'evidence', 'fix_write_set', 'size'])
    expect(t.rows.map(r => r.line)).toEqual([1, 2])
    expect(t.rows[1]?.cells).toEqual([1013, 'Re-forking fails', 'FIXED', 'x', [], 'S'])
    expect(t.errors).toEqual([])
  })

  test('a key a later entry adds becomes a column; earlier rows leave it empty', async () => {
    const t = parseJsonl('{"a": 1}\n{"b": 2, "a": 3}')
    expect(t.columns).toEqual(['a', 'b'])
    expect(t.rows.map(r => r.cells)).toEqual([
      [1, undefined],
      [3, 2],
    ])
  })

  test('a line that fails to parse is an error naming its line, never dropped', async () => {
    const t = parseJsonl('{"a": 1}\n{"a": \n[1, 2]\n{"a": 4}')
    expect(t.rows.map(r => r.line)).toEqual([1, 4])
    expect(t.errors.map(e => e.line)).toEqual([2, 3])
    expect(t.errors[1]?.message).toBe('not a JSON object: [1,2]')
  })

  test('blank lines carry no entry and no error', async () => {
    const t = parseJsonl('\n{"a": 1}\n   \n')
    expect(t.rows.map(r => r.line)).toEqual([2])
    expect(t.errors).toEqual([])
  })
})

describe('compact', () => {
  test('draws strings bare, nested values as one-line JSON, a missing key empty', async () => {
    expect(compact('x')).toBe('x')
    expect(compact(['a', 'b'])).toBe('["a","b"]')
    expect(compact({ k: { n: 1 } })).toBe('{"k":{"n":1}}')
    expect(compact(null)).toBe('null')
    expect(compact(undefined)).toBe('')
  })
})

describe('toMarkdown', () => {
  test('escapes pipes and counts the rows left out', async () => {
    const md = toMarkdown(parseJsonl(INVENTORY), 1)
    expect(md.split('\n')).toEqual([
      '| line | title | status | evidence | fix_write_set | size |',
      '| --- | --- | --- | --- | --- | --- |',
      '| 1002 | Fork binds lease | UNSURE | a \\| b | ["internal/job/checkout.go"] | M |',
      '',
      '1 more rows not shown.',
    ])
  })

  test('cuts a long cell with an ellipsis', async () => {
    const md = toMarkdown(parseJsonl(JSON.stringify({ a: 'x'.repeat(500) })), 10)
    const cell = md.split('\n')[2] ?? ''
    expect(cell.endsWith('… |')).toBe(true)
    expect(cell.length).toBe('| '.length + 160 + ' |'.length)
  })

  test('draws nothing for a file with no entry', async () => {
    expect(toMarkdown(parseJsonl('not json'), 10)).toBe('')
  })
})

describe('jsonlPaths', () => {
  test('reads link targets, code spans and bare paths once each', async () => {
    const text = [
      'See [part 4](/Users/e/Application%20Support/notes/part-4.jsonl) and',
      '`/Users/e/Application Support/notes/part-4.jsonl`, then ~/x/runs.jsonl.',
      'Also `rows.jsonl` but not report.json or /tmp/a.jsonlx.',
    ].join('\n')
    expect(jsonlPaths(text)).toEqual(['/Users/e/Application Support/notes/part-4.jsonl', 'rows.jsonl', '~/x/runs.jsonl'])
  })

  test('strips a file: scheme from a link', async () => {
    expect(jsonlPaths('[a](file:///tmp/a.jsonl)')).toEqual(['/tmp/a.jsonl'])
  })
})

describe('resolvePath', () => {
  test('expands home and joins a relative path to cwd', async () => {
    expect(resolvePath('~/a.jsonl', '/Users/e', '/w')).toBe('/Users/e/a.jsonl')
    expect(resolvePath('/abs/a.jsonl', '/Users/e', '/w')).toBe('/abs/a.jsonl')
    expect(resolvePath('rel/a.jsonl', '/Users/e', '/w/')).toBe('/w/rel/a.jsonl')
  })
})
