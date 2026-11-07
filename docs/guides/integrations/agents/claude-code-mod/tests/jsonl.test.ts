import { describe, expect, test } from 'claude-code/testing'

import { jsonlPaths, parseJsonl, resolvePath, toMarkdown } from '../hooks/jsonl'

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

describe('toMarkdown', () => {
  test('escapes pipes and counts the rows left out', async () => {
    const md = toMarkdown(parseJsonl(INVENTORY), 1, 20)
    expect(md.split('\n')).toEqual([
      '| line | title | status | evidence | fix\\_write\\_set | size |',
      '| --- | --- | --- | --- | --- | --- |',
      '| 1002 | Fork binds lease | UNSURE | a \\| b | \\["internal/job/checkout.go"\\] | M |',
      '',
      '1 more rows not shown.',
    ])
  })

  test('cuts a long cell with an ellipsis', async () => {
    const md = toMarkdown(parseJsonl(JSON.stringify({ a: 'x'.repeat(500) })), 10, 20)
    const cell = md.split('\n')[2] ?? ''
    expect(cell.endsWith('… |')).toBe(true)
    expect(cell.length).toBe('| '.length + 160 + ' |'.length)
  })

  test('draws nothing for a file with no entry', async () => {
    expect(toMarkdown(parseJsonl('not json'), 10, 20)).toBe('')
  })
})

describe('jsonlPaths', () => {
  test('reads link targets, code spans and bare paths once each', async () => {
    const text = [
      'See [part 4](/Users/e/Application%20Support/notes/part-4.jsonl) and',
      '`/Users/e/Application Support/notes/part-4.jsonl`, then ~/x/runs.jsonl.',
      'Also `rows.jsonl` but not report.json or /tmp/a.jsonlx.',
    ].join('\n')
    expect(jsonlPaths(text)).toEqual([
      '/Users/e/Application Support/notes/part-4.jsonl',
      'rows.jsonl',
      '~/x/runs.jsonl',
    ])
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

describe('parseJsonl edge cases', () => {
  test('a key named like an Object.prototype member is a column, and a row without it has no cell', async () => {
    const t = parseJsonl('{"toString": 1}\n{"a": 2}')
    expect(t.columns).toEqual(['toString', 'a'])
    expect(t.rows[1]?.cells).toEqual([undefined, 2])
    expect(toMarkdown(t, 10, 20).split('\n')[3]).toBe('|  | 2 |')
  })

  test('an error names at most 80 characters of the value it rejected', async () => {
    const t = parseJsonl(JSON.stringify(['x'.repeat(500)]))
    expect(t.errors[0]?.message.length).toBe('not a JSON object: '.length + 80)
  })
})

describe('toMarkdown edge cases', () => {
  test('past 20 columns the rest are left out and counted', async () => {
    const entry = Object.fromEntries(Array.from({ length: 23 }, (_, i) => [`c${i}`, i]))
    const lines = toMarkdown(parseJsonl(JSON.stringify(entry)), 10, 20).split('\n')
    expect(lines[0]?.split(' | ').length).toBe(20)
    expect(lines.at(-1)).toBe('3 more columns not shown.')
  })

  test('escapes the characters markdown would read as syntax', async () => {
    const md = toMarkdown(parseJsonl(JSON.stringify({ a: '[x](y) <b> `c` *d* _e_ !f &g' })), 10, 20)
    expect(md.split('\n')[2]).toBe('| \\[x\\]\\(y\\) \\<b\\> \\`c\\` \\*d\\* \\_e\\_ \\!f \\&g |')
  })

  test('cuts by code point, never inside a surrogate pair', async () => {
    const md = toMarkdown(parseJsonl(JSON.stringify({ a: '😀'.repeat(300) })), 10, 20)
    const body = (md.split('\n')[2] ?? '').slice(2, -2)
    expect([...body].length).toBe(160)
    expect(body.endsWith('😀…')).toBe(true)
  })
})

describe('jsonlPaths edge cases', () => {
  test('a path that only starts like a .jsonl file is no match', async () => {
    expect(jsonlPaths('Kept /tmp/a.jsonl.bak and /tmp/b.jsonl.')).toEqual(['/tmp/b.jsonl'])
  })

  test('a code span with a command or a glob names no path', async () => {
    expect(jsonlPaths('Run `jq . a.jsonl` or `ls *.jsonl` or `out/{a,b}.jsonl`.')).toEqual([])
  })

  test('a link with a scheme other than file: is skipped', async () => {
    expect(jsonlPaths('[a](https://example.com/a.jsonl) [b](mailto:x/b.jsonl)')).toEqual([])
  })
})

describe('resolvePath edge cases', () => {
  test('folds . and .. segments and doubled slashes', async () => {
    expect(resolvePath('./a/../b//c.jsonl', '/h', '/w')).toBe('/w/b/c.jsonl')
    expect(resolvePath('../c.jsonl', '/h', '/w/x')).toBe('/w/c.jsonl')
    expect(resolvePath('/a/./b/../c.jsonl', '/h', '/w')).toBe('/a/c.jsonl')
    expect(resolvePath('~/a/../b.jsonl', '/h', '/w')).toBe('/h/b.jsonl')
  })
})
