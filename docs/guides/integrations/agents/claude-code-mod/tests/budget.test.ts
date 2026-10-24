import { describe, expect, test } from 'claude-code/testing'

import { budgetStatus, holderText, parseBudget, recentRefusal, REFUSAL_SHOW_MS, refusalIn, refusalText, runsMagus } from '../hooks/budget'

const SINCE = '2026-10-24T09:40:55Z'
const AT = Date.parse(SINCE)

const STATUS = JSON.stringify({
  pool: { capacity: 10, queued: 0 },
  broker: {
    pid: 51051,
    capacity: {
      budget_mb: 24064,
      held_mb: 8940,
      budget_slots: 10,
      held_slots: 8,
      holders: [
        {
          project: '.',
          target: 'go-test',
          pid: 21068,
          memory_mb: 4470,
          slots: 4,
          dir: '/Users/e/Repos/magus-integrate-harness-11',
          command: './magus run go-test . -s',
          since: SINCE,
        },
        { project: 'console', target: 'test', pid: 23224, memory_mb: 512, slots: 1 },
      ],
    },
  },
})

describe('parseBudget', () => {
  test('reads capacity and every holder from broker.capacity', async () => {
    const p = parseBudget(STATUS)
    expect(p.ok).toBe(true)
    if (!p.ok) return
    expect(p.budget).toMatchObject({ budgetSlots: 10, heldSlots: 8, budgetMb: 24064, heldMb: 8940 })
    expect(p.budget?.holders).toEqual([
      {
        pid: 21068,
        project: '.',
        target: 'go-test',
        dir: '/Users/e/Repos/magus-integrate-harness-11',
        command: './magus run go-test . -s',
        slots: 4,
        memoryMb: 4470,
        since: AT,
      },
      { pid: 23224, project: 'console', target: 'test', dir: '', command: '', slots: 1, memoryMb: 512, since: 0 },
    ])
  })

  test('no broker is a budget nothing holds, not an error', async () => {
    expect(parseBudget(JSON.stringify({ pool: { capacity: 10 } }))).toEqual({ ok: true, budget: null })
  })

  test('an idle broker leaves its zero counts out, which read as 0', async () => {
    const p = parseBudget(JSON.stringify({ broker: { capacity: { budget_mb: 24064, budget_slots: 10 } } }))
    expect(p).toEqual({ ok: true, budget: { budgetSlots: 10, heldSlots: 0, budgetMb: 24064, heldMb: 0, holders: [] } })
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
})

describe('render', () => {
  const p = parseBudget(STATUS)
  const budget = p.ok ? p.budget : null

  test('the status line names slots and memory in GiB', async () => {
    expect(budgetStatus({ budget, readAt: 0 })).toBe('budget 8/10 slots, 8.7/23.5 GiB')
    expect(budgetStatus({ budget: null, readAt: 0 })).toBe('budget idle')
    expect(budgetStatus(null)).toBeUndefined()
  })

  test('a holder line names pid, target, slots, memory, age and checkout', async () => {
    const lines = (budget?.holders ?? []).map(h => holderText(h, AT + 3 * 60_000))
    expect(lines).toEqual([
      'pid 21068 · go-test · 4 slots · 4.4 GiB · held 3m · in magus-integrate-harness-11',
      'pid 23224 · console test · 1 slot · 0.5 GiB',
    ])
  })
})

describe('refusals', () => {
  const output = [
    'building',
    '[MGS3009] not starting (root) ci: this machine\'s build budget is full; held by pid 21068 (root) go-test',
    'see: MGS3009',
  ].join('\n')

  test('a magus command is told from one that only mentions it', async () => {
    expect(runsMagus('./magus run go-test .')).toBe(true)
    expect(runsMagus('cd /w && ./magus run lint .')).toBe(true)
    expect(runsMagus('/usr/local/bin/magus status')).toBe(true)
    expect(runsMagus('magus')).toBe(true)
    expect(runsMagus('ls magusfile.buzz')).toBe(false)
    expect(runsMagus('grep magus-run notes.md')).toBe(false)
  })

  test('the MGS3009 line is the refusal; output without it is none', async () => {
    expect(refusalIn('  ./magus run ci . ', output, 5)).toEqual({
      at: 5,
      command: './magus run ci .',
      line: "[MGS3009] not starting (root) ci: this machine's build budget is full; held by pid 21068 (root) go-test",
    })
    expect(refusalIn('./magus run ci .', 'ok\nMGS3009 is documented', 5)).toBeNull()
  })

  test('the band shows the newest refusal inside its window, with the budget', async () => {
    const r = { at: AT, command: './magus run ci .', line: '[MGS3009] not starting (root) ci' }
    expect(recentRefusal([r], AT + REFUSAL_SHOW_MS)).toBe(r)
    expect(recentRefusal([r], AT + REFUSAL_SHOW_MS + 1)).toBeNull()
    expect(recentRefusal([], AT)).toBeNull()
    const p = parseBudget(STATUS)
    const read = { budget: p.ok ? p.budget : null, readAt: AT }
    expect(refusalText(r, read, AT + 2 * 60_000)).toBe('MGS3009 refused `./magus run ci .` 2m ago: budget 8/10 slots, 8.7/23.5 GiB')
    expect(refusalText(r, null, AT)).toBe('MGS3009 refused `./magus run ci .` 0s ago')
  })
})
