import { describe, expect, test } from 'claude-code/testing'

import { candidateJobs, connectError, parseActivity, parseJobs, parseRuns, roleOf, socketFromStatus } from '../hooks/api'

describe('socketFromStatus', () => {
  test('a running server answers its socket without the scheme', async () => {
    const out = JSON.stringify({ server: { pid: 1 }, pool: { socket: 'unix:///c/magus/run/server.sock' } })
    expect(socketFromStatus(out)).toBe('/c/magus/run/server.sock')
  })

  test('no server, or output that is not a report, answers null', async () => {
    expect(socketFromStatus(JSON.stringify({ pool_error: 'dial unix: no such file' }))).toBeNull()
    expect(socketFromStatus('no server is running')).toBeNull()
  })
})

test('connectError reads a Connect error body, and falls back to the status', async () => {
  expect(connectError(403, '{"code":"permission_denied","message":"socket peer is not the server\'s user"}')).toBe(
    "permission_denied: socket peer is not the server's user",
  )
  expect(connectError(502, '')).toBe('HTTP 502')
})

describe('parseJobs', () => {
  test('reads protojson: int64 as strings, enum holders, zero values left out', async () => {
    const parsed = parseJobs(
      JSON.stringify({
        jobs: [
          { name: 'jobs/sync-graph', id: 'sync-graph', holder: 'JOB_HOLDER_SERVER', state: 'pass', description: 'reconcile the graph' },
          {
            id: 'api/store',
            parent: 'api',
            holder: 'JOB_HOLDER_SESSION',
            state: 'running',
            model: 'opus',
            check: 'magus run go-test api',
            writePaths: ['api/store.go'],
            goals: [{ id: 'gone', kind: 'symbol' }],
            writeProof: 'disjoint',
            baseVerdict: 'diverged',
            endReason: '',
            updated: '1700000000',
          },
        ],
        overdue: ['api/store'],
        blocked: [{ job: 'api/later', on: 'api/store', state: 'running' }],
      }),
      42,
    )
    if (parsed.error) return expect(parsed.error.message).toBe('')
    const listing = parsed.value
    expect(listing.fetchedAt).toBe(42)
    expect(listing.overdue).toEqual(['api/store'])
    expect(listing.stale).toEqual([])
    expect(listing.blocked).toEqual([{ job: 'api/later', on: 'api/store', state: 'running' }])
    expect(listing.rows[0]).toMatchObject({ id: 'sync-graph', holder: 'server', criteria: 'reconcile the graph', updated: 0 })
    expect(listing.rows[1]).toEqual({
      id: 'api/store',
      parent: 'api',
      state: 'running',
      model: 'opus',
      holder: '',
      criteria: '',
      check: 'magus run go-test api',
      writePaths: ['api/store.go'],
      goals: ['symbol gone'],
      proof: 'disjoint',
      endReason: '',
      baseVerdict: 'diverged',
      updated: 1700000000,
    })
  })
})

test('parseRuns reads invocations; a running one has no end', async () => {
  const parsed = parseRuns(
    JSON.stringify({
      invocations: [
        { id: 'inv1', command: { arguments: ['run', 'go-build', '.'] }, startTime: '2026-10-09T23:56:00Z', status: 'STATUS_PASS', endTime: '2026-10-09T23:56:33Z' },
        { id: 'inv2', command: { arguments: ['affected', 'ci'] }, startTime: '2026-10-09T23:57:00Z' },
      ],
    }),
  )
  if (parsed.error) return expect(parsed.error.message).toBe('')
  const runs = parsed.value
  expect(runs[0]).toEqual({ id: 'inv1', command: 'run go-build .', startedAt: Date.parse('2026-10-09T23:56:00Z'), endedAt: Date.parse('2026-10-09T23:56:33Z'), status: 'pass' })
  expect(runs[1]?.endedAt).toBe(0)
  expect(runs[1]?.status).toBe('')
})

test('parseActivity reads events with their kind and outcome words', async () => {
  const parsed = parseActivity(
    JSON.stringify({
      events: [{ time: '2026-10-09T23:56:00Z', kind: 'KIND_FILE_CHANGE', action: 'write', outcome: 'OUTCOME_ERROR', actor: 'eli', preview: 'api/store.go' }],
    }),
  )
  if (parsed.error) return expect(parsed.error.message).toBe('')
  const lines = parsed.value
  expect(lines).toEqual([
    { at: Date.parse('2026-10-09T23:56:00Z'), kind: 'file_change', action: 'write', outcome: 'error', actor: 'eli', preview: 'api/store.go' },
  ])
})

test('a body that is not JSON, or a state the pane has no word for, fails with what was wrong', async () => {
  const garbled = parseJobs('<html>', 0)
  expect(garbled.error?.message ?? '').toContain('ListJobs answered a body that is not JSON')
  const unknown = parseJobs(JSON.stringify({ jobs: [{ id: 'a', state: 'paused' }] }), 0)
  expect(unknown.error?.message ?? '').toBe('ListJobs answered an unknown job state paused')
  const runs = parseRuns('[]')
  expect(runs.error?.message ?? '').toBe('ListInvocations answered JSON that is not an object')
})

test('candidateJobs names the bare job, then the one under the parent, as the guard resolves them', async () => {
  expect(candidateJobs('api/fix store')).toEqual(['store', 'api/store'])
  expect(candidateJobs('css-reorg/sub/feat tokens')).toEqual(['tokens', 'css-reorg/sub/tokens'])
  expect(candidateJobs('Map magus data feeds')).toEqual([])
})

describe('roleOf', () => {
  test('reads the role a spawn description names after its last slash', async () => {
    expect(roleOf('root/orchestrator plan')).toBe('orchestrator')
    expect(roleOf('root/sub/integrator land')).toBe('integrator')
    expect(roleOf('api/worker store')).toBe('worker')
  })

  test('a role outside the three, or a description of another shape, names none', async () => {
    expect(roleOf('api/fix store')).toBeNull()
    expect(roleOf('root/worker')).toBeNull()
    expect(roleOf('Map magus data feeds')).toBeNull()
  })
})
