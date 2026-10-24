import { describe, expect, test } from 'claude-code/testing'

import {
  budgetStatus,
  holderText,
  isBoard,
  parseBudget,
  recentRefusal,
  REFUSAL_SHOW_MS,
  refusalText,
  runsMagus,
  waiterText,
} from '../hooks/budget'

const SINCE = '2026-10-24T09:40:55Z'
const AT = Date.parse(SINCE)

const GO_TEST = {
  project: '.',
  target: 'go-test',
  pid: 21068,
  memory_mb: 4470,
  slots: 4,
  dir: '/Users/e/Repos/magus-integrate-harness-11',
  command: './magus run go-test . -s',
  since: SINCE,
  owner: 'fix-cache',
}

const STATUS = JSON.stringify({
  pool: { capacity: 10, queued: 0 },
  broker: {
    pid: 51051,
    capacity: {
      budget_mb: 24064,
      held_mb: 8940,
      budget_slots: 10,
      held_slots: 8,
      holders: [GO_TEST, { project: 'console', target: 'test', pid: 23224, memory_mb: 512, slots: 1 }],
      waiting: [
        { pid: 30001, project: '.', target: 'lint', slots: 2, dir: '/w/magus-a', owner: 'unleased', since: SINCE },
        { pid: 30002, project: '.', target: 'ci', owner: 'job-b', reservation: true, since: SINCE },
        { pid: 30003, project: 'docs', target: 'ci', owner: 'job-c', tree: 'root-job', since: SINCE },
      ],
      refused: [
        {
          at: SINCE,
          pid: 30100,
          project: '.',
          target: 'ci',
          slots: 4,
          dir: '/w/magus-b',
          command: './magus run ci .',
          owner: 'spawned by root',
          fits: true,
          holders: [GO_TEST],
        },
      ],
    },
  },
})

const HOLDER = {
  pid: 21068,
  project: '.',
  target: 'go-test',
  dir: '/Users/e/Repos/magus-integrate-harness-11',
  command: './magus run go-test . -s',
  slots: 4,
  memoryMb: 4470,
  since: AT,
  owner: 'fix-cache',
}

describe('parseBudget', () => {
  test('reads capacity, every holder with its owner, the waiters and the refusals', async () => {
    const p = parseBudget(STATUS)
    expect(p.ok).toBe(true)
    if (!p.ok) return
    expect(p.budget).toMatchObject({ budgetSlots: 10, heldSlots: 8, budgetMb: 24064, heldMb: 8940 })
    expect(p.budget?.holders).toEqual([
      HOLDER,
      { pid: 23224, project: 'console', target: 'test', dir: '', command: '', slots: 1, memoryMb: 512, since: 0, owner: '' },
    ])
    expect(p.budget?.waiting).toEqual([
      { pid: 30001, project: '.', target: 'lint', dir: '/w/magus-a', command: '', slots: 2, memoryMb: 0, owner: 'unleased', reservation: false, tree: '', since: AT },
      { pid: 30002, project: '.', target: 'ci', dir: '', command: '', slots: 0, memoryMb: 0, owner: 'job-b', reservation: true, tree: '', since: AT },
      { pid: 30003, project: 'docs', target: 'ci', dir: '', command: '', slots: 0, memoryMb: 0, owner: 'job-c', reservation: false, tree: 'root-job', since: AT },
    ])
    expect(p.budget?.refused).toEqual([
      {
        at: AT,
        pid: 30100,
        project: '.',
        target: 'ci',
        dir: '/w/magus-b',
        command: './magus run ci .',
        slots: 4,
        memoryMb: 0,
        owner: 'spawned by root',
        fits: true,
        holders: [HOLDER],
      },
    ])
  })

  test('no broker is a budget nothing holds, not an error', async () => {
    expect(parseBudget(JSON.stringify({ pool: { capacity: 10 } }))).toEqual({ ok: true, budget: null })
  })

  test('an idle broker leaves its zero counts and empty lists out, which read as 0 and none', async () => {
    const p = parseBudget(JSON.stringify({ broker: { capacity: { budget_mb: 24064, budget_slots: 10 } } }))
    expect(p).toEqual({
      ok: true,
      budget: { budgetSlots: 10, heldSlots: 0, budgetMb: 24064, heldMb: 0, holders: [], waiting: [], refused: [] },
    })
  })

  test('output that is not the status record is an error that says so', async () => {
    const p = parseBudget('[error] the workspace did not load')
    expect(p.ok).toBe(false)
    if (p.ok) return
    expect(p.error).toMatch(/^magus status -o json printed no JSON: \[error\] the workspace did not load$/)
    expect(parseBudget(JSON.stringify({ broker: { pid: 1 } }))).toEqual({
      ok: false,
      error: 'magus status -o json has a broker without broker.capacity',
    })
  })

  test('a budget read back from state is told from one without waiters and refusals', async () => {
    const p = parseBudget(STATUS)
    expect(p.ok && p.budget !== null && isBoard(p.budget)).toBe(true)
    expect(isBoard({ budgetSlots: 1, heldSlots: 0, budgetMb: 0, heldMb: 0, holders: [] })).toBe(false)
  })
})

describe('render', () => {
  const p = parseBudget(STATUS)
  const budget = p.ok ? p.budget : null

  test('the status line names slots and memory in GiB', async () => {
    expect(budgetStatus({ budget, readAt: 0 })).toBe('budget 8/10 slots, 8.7/23.5 GiB')
    expect(budgetStatus({ budget: null, readAt: 0 })).toBe('budget idle')
    expect(budgetStatus(null)).toBeUndefined()
  })

  test('a holder line names pid, target, owner, slots, memory, age and checkout', async () => {
    const lines = (budget?.holders ?? []).map(h => holderText(h, AT + 3 * 60_000))
    expect(lines).toEqual([
      'pid 21068 · go-test · fix-cache · 4 slots · 4.4 GiB · held 3m · in magus-integrate-harness-11',
      'pid 23224 · console test · 1 slot · 0.5 GiB',
    ])
  })

  test('a waiter line names pid, target, owner, why and how long', async () => {
    const lines = (budget?.waiting ?? []).map(w => waiterText(w, AT + 60_000))
    expect(lines).toEqual([
      'pid 30001 · lint · unleased · 2 slots · waiting 1m · in magus-a',
      'pid 30002 · ci · job-b · 1 slot · reserving · waiting 1m',
      "pid 30003 · docs ci · job-c · 1 slot · for root-job's seat · waiting 1m",
    ])
  })
})

describe('refusals', () => {
  const p = parseBudget(STATUS)
  const budget = p.ok ? p.budget : null

  test('a magus command is told from one that only mentions it', async () => {
    expect(runsMagus('./magus run go-test .')).toBe(true)
    expect(runsMagus('cd /w && ./magus run lint .')).toBe(true)
    expect(runsMagus('/usr/local/bin/magus status')).toBe(true)
    expect(runsMagus('magus')).toBe(true)
    expect(runsMagus('ls magusfile.buzz')).toBe(false)
    expect(runsMagus('grep magus-run notes.md')).toBe(false)
  })

  test('the band shows the newest broker refusal inside its window, with the budget', async () => {
    const r = budget?.refused[0]
    expect(recentRefusal(budget, AT + REFUSAL_SHOW_MS)).toBe(r)
    expect(recentRefusal(budget, AT + REFUSAL_SHOW_MS + 1)).toBeNull()
    expect(recentRefusal(null, AT)).toBeNull()
    if (r === undefined) return
    expect(refusalText(r, budget, AT + 2 * 60_000)).toBe(
      'MGS3009 refused ci for spawned by root, pid 30100, in magus-b 2m ago: budget 8/10 slots, 8.7/23.5 GiB',
    )
    expect(refusalText({ ...r, fits: false, owner: '', dir: '' }, null, AT)).toBe('MGS3009 refused ci (never fits) for pid 30100 0s ago')
  })
})
