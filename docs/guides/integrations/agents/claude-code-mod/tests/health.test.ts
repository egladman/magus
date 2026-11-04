import { describe, expect, test } from 'claude-code/testing'

import { assess, BOOTSTRAP, inBand, statusText } from '../hooks/health'
import type { Probe } from '../hooks/health'

const healthy: Probe = {
  isMagusSource: true,
  hasLocalBinary: true,
  pathBinary: '/usr/local/bin/magus',
  binary: { path: '/w/magus', version: 'v1-g94d434ac0', commit: '94d434ac0e0a' },
  head: '94d434ac0e0a',
  behind: null,
  server: { isRunning: true, version: 'v1-g94d434ac0', mcpState: 'serving', mcpNote: '' },
}

describe('assess', () => {
  test('a healthy workspace has no issues', async () => {
    expect(assess(healthy)).toEqual([])
    expect(statusText([])).toBeUndefined()
  })

  test('no binary anywhere means the guard is off, and nothing else is judged', async () => {
    const issues = assess({ ...healthy, hasLocalBinary: false, pathBinary: null, binary: null, server: null })
    expect(issues.map(i => i.id)).toEqual(['guard-off'])
    expect(issues[0]?.level).toBe('error')
    expect(issues[0]?.fix).toBe(BOOTSTRAP)
  })

  test('a borrowed or stale binary is not judged: the guard names the rebuild to the agent', async () => {
    const borrowed = assess({ ...healthy, hasLocalBinary: false, binary: { ...healthy.binary!, commit: '6eadd1057da4' } })
    expect(borrowed).toEqual([])
    expect(assess({ ...healthy, binary: { ...healthy.binary!, commit: '6eadd1057da4' }, behind: 1 })).toEqual([])
  })

  test('another workspace on a PATH binary is fine', async () => {
    expect(assess({ ...healthy, isMagusSource: false, hasLocalBinary: false })).toEqual([])
  })

  test('a server that is down hides the skew and MCP checks', async () => {
    const issues = assess({ ...healthy, server: { isRunning: false, version: '', mcpState: 'unreachable', mcpNote: '' } })
    expect(issues.map(i => i.id)).toEqual(['server-down'])
  })

  test('server skew is an error; an MCP endpoint not serving is a warning', async () => {
    const issues = assess({
      ...healthy,
      server: { isRunning: true, version: 'v0-old', mcpState: 'not-ready', mcpNote: 'no workspace loaded' },
    })
    expect(issues.map(i => [i.id, i.level])).toEqual([
      ['server-skew', 'error'],
      ['mcp', 'warn'],
    ])
    expect(statusText(issues)).toBe('⚠ server skew, mcp not-ready')
  })

  test('only the guard failing open and a server on another version reach the band', async () => {
    const down = assess({ ...healthy, server: { isRunning: false, version: '', mcpState: '', mcpNote: '' } })
    const skew = assess({ ...healthy, server: { isRunning: true, version: 'v0-old', mcpState: 'not-ready', mcpNote: '' } })
    const off = assess({ ...healthy, hasLocalBinary: false, pathBinary: null, binary: null, server: null })
    expect(inBand(down)).toEqual([])
    expect(inBand(skew).map(i => i.id)).toEqual(['server-skew'])
    expect(inBand(off).map(i => i.id)).toEqual(['guard-off'])
  })
})
