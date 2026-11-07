import { describe, expect, test } from 'claude-code/testing'

import { REF_DOC, refArgs, refCommand, refLinks, refOf, refsIn, stripAnsi } from '../hooks/refs'

describe('refsIn', () => {
  test('finds output refs, guard details and invocation ids once each, in order', async () => {
    const text = 'output: out41faa93d10d7\ndetails: ./magus query output grd26b6cf2f8904ccf5\ninv: invmsm3vcou1 then out41faa93d10d7 again'
    expect(refsIn(text)).toEqual(['out41faa93d10d7', 'grd26b6cf2f8904ccf5', 'invmsm3vcou1'])
  })

  test('leaves words that only start like a ref', async () => {
    expect(refsIn('output outline outdated invalid inventory grdx')).toEqual([])
    expect(refsIn('out1234')).toEqual([])
  })
})

describe('refCommand', () => {
  test('an output ref or guard detail prints with query output; an invocation with query invocation', async () => {
    expect(refCommand('out41faa93d10d7')).toBe('./magus query output out41faa93d10d7')
    expect(refCommand('grd26b6cf2f8904ccf5')).toBe('./magus query output grd26b6cf2f8904ccf5')
    expect(refArgs('invmsm3vcou1')).toEqual(['query', 'invocation', 'invmsm3vcou1'])
  })
})

describe('refLinks', () => {
  test('draws each ref as a link titled with its command, and reads the ref back', async () => {
    expect(refLinks(['out41faa93d10d7'])).toBe(`[out41faa93d10d7](${REF_DOC}#out41faa93d10d7 "./magus query output out41faa93d10d7")`)
    expect(refOf(`${REF_DOC}#out41faa93d10d7`)).toBe('out41faa93d10d7')
  })

  test('a link refLinks did not draw names no ref', async () => {
    expect(refOf('https://example.com/#out41faa93d10d7')).toBeNull()
    expect(refOf(`${REF_DOC}#out41faa93d10d7x`)).toBeNull()
  })
})

describe('stripAnsi', () => {
  test('drops color codes and keeps the text', async () => {
    expect(stripAnsi('\x1b[31m[fail]\x1b[0m lint')).toBe('[fail] lint')
  })
})
