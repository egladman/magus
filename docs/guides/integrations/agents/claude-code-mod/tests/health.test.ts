import { describe, expect, test } from 'claude-code/testing'

import { assess, BOOTSTRAP, signature, statusText } from '../hooks/health'
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

  test('the magus source tree without ./magus is running a borrowed binary', async () => {
    const issues = assess({
      ...healthy,
      hasLocalBinary: false,
      binary: { path: '/usr/local/bin/magus', version: 'v1-g6eadd1057', commit: '6eadd1057da4' },
      server: { ...healthy.server!, version: 'v1-g6eadd1057' },
    })
    expect(issues.map(i => i.id)).toEqual(['borrowed-binary'])
    expect(issues[0]?.title).toContain('/usr/local/bin/magus')
    expect(issues[0]?.title).toContain('6eadd1057')
  })

  test('another workspace on a PATH binary is fine', async () => {
    expect(assess({ ...healthy, isMagusSource: false, hasLocalBinary: false })).toEqual([])
  })

  test('a binary behind HEAD counts the commits', async () => {
    const issues = assess({ ...healthy, binary: { ...healthy.binary!, commit: '6eadd1057da4' }, behind: 1 })
    expect(issues.map(i => i.id)).toEqual(['stale-binary'])
    expect(issues[0]?.title).toBe('./magus is 1 commit behind HEAD')
  })

  test('a binary off HEAD\'s history names both commits', async () => {
    const issues = assess({ ...healthy, binary: { ...healthy.binary!, commit: 'aaaaaaaaaaaa' }, behind: null })
    expect(issues[0]?.title).toBe('./magus is built from aaaaaaaaa, HEAD is 94d434ac0')
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

  test('the signature changes when an issue title changes', async () => {
    const one = assess({ ...healthy, binary: { ...healthy.binary!, commit: 'x' }, behind: 1 })
    const two = assess({ ...healthy, binary: { ...healthy.binary!, commit: 'x' }, behind: 2 })
    expect(signature(one)).not.toBe(signature(two))
  })
})
