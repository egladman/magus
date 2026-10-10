import { describe, expect, test } from 'claude-code/testing'

import { age, leaf, treeLines } from '../hooks/tree'
import type { JobRow } from '../hooks/tree'

const NOW = 1_000_000
const row = (id: string, parent: string, state: JobRow['state'], updated = NOW - 60): JobRow => ({
  id,
  parent,
  state,
  model: '',
  holder: '',
  criteria: '',
  check: '',
  writePaths: [],
  goals: [],
  proof: '',
  endReason: '',
  baseVerdict: '',
  updated,
})

const ids = (rows: JobRow[], filter: 'live' | 'recent' | 'all') =>
  treeLines(rows, filter, NOW).map(l => `${'  '.repeat(l.depth)}${l.row.id}${l.isContext ? ' (ctx)' : ''}`)

describe('treeLines', () => {
  test('live keeps running work and draws its finished ancestors as context', async () => {
    const rows = [
      row('root', '', 'pass'),
      row('root/a', 'root', 'running'),
      row('root/b', 'root', 'pass'),
      row('old', '', 'no_return'),
    ]
    expect(ids(rows, 'live')).toEqual(['root (ctx)', '  root/a'])
  })

  test('live leaves out stale rows; all still shows them', async () => {
    const rows = [row('fresh', '', 'running'), row('forgotten', '', 'declared')]
    expect(treeLines(rows, 'live', NOW, new Set(['forgotten'])).map(l => l.row.id)).toEqual(['fresh'])
    expect(treeLines(rows, 'all', NOW, new Set(['forgotten'])).map(l => l.row.id)).toEqual(['forgotten', 'fresh'])
  })

  test('a row whose parent is missing from the store is a root, not hidden', async () => {
    expect(ids([row('orphan/x', 'orphan', 'declared')], 'live')).toEqual(['orphan/x'])
  })

  test('recent keeps the last day; all keeps everything, newest first', async () => {
    const rows = [row('a', '', 'pass', NOW - 10), row('b', '', 'pass', NOW - 200_000), row('c', '', 'fail', NOW - 5)]
    expect(ids(rows, 'recent')).toEqual(['c', 'a'])
    expect(ids(rows, 'all')).toEqual(['c', 'a', 'b'])
  })
})

test('leaf drops the parent prefix the indentation already shows', async () => {
  expect(leaf('api/store', 'api')).toBe('store')
  expect(leaf('other', 'api')).toBe('other')
  expect(age(90)).toBe('2m')
  expect(age(3 * 86400)).toBe('3d')
})
