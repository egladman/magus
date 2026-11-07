import { describe, expect, test } from 'claude-code/testing'

import { REF_DOC, refArgs, refCommand, refLinks, refOf, refStrings, refsIn, stripAnsi } from '../hooks/refs'

describe('refsIn', () => {
  test('finds output refs, guard details and invocation ids once each, in order', async () => {
    const text =
      'output: out41faa93d10d7\ndetails: ./magus query output grd26b6cf2f8904ccf5\ninv: invmsm3vcou1 then out41faa93d10d7 again'
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
    expect(refLinks(['out41faa93d10d7'])).toBe(
      `[out41faa93d10d7](${REF_DOC}#out41faa93d10d7 "./magus query output out41faa93d10d7")`,
    )
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

describe('the ref grammar magus itself uses', () => {
  test('a minted output ref has exactly 12 or 8 hex digits', async () => {
    expect(refsIn('out41faa93d10d7 out41faa93d')).toEqual(['out41faa93d10d7', 'out41faa93d'])
    expect(refsIn('out41faa93d10 out41faa93d10d7ab out41faa9')).toEqual([])
  })

  test('an invocation id needs a digit, so words that start with inv are not refs', async () => {
    expect(refsIn('investigate invocations invalid inv inv1')).toEqual(['inv1'])
  })

  test('a guard detail is grd and exactly 16 hex digits, and prints with query output', async () => {
    expect(refsIn('grd26b6cf2f8904ccf5 grd26b6cf2f8904cc grd26b6cf2f8904ccf5a')).toEqual(['grd26b6cf2f8904ccf5'])
    expect(refArgs('grd26b6cf2f8904ccf5')).toEqual(['query', 'output', 'grd26b6cf2f8904ccf5'])
  })

  test('a ref at the start of a line is found in the strings of an output', async () => {
    const output = { stdout: 'out41faa93d10d7\ngrd26b6cf2f8904ccf5 and invmsm3vcou1', stderr: [''] }
    expect(refsIn(refStrings(output).join('\n'))).toEqual(['out41faa93d10d7', 'grd26b6cf2f8904ccf5', 'invmsm3vcou1'])
  })
})

describe('refCommand', () => {
  test('names the program the command ran with', async () => {
    expect(refCommand('invmsm3vcou1', 'magus')).toBe('magus query invocation invmsm3vcou1')
  })
})

describe('stripAnsi sequences', () => {
  test('drops cursor moves and hyperlinks as well as colors', async () => {
    expect(stripAnsi('\x1b[2K\x1b[1Gdone \x1b]8;;https://x.test\x07link\x1b]8;;\x07 \x1b]0;t\x1b\\ok')).toBe(
      'done link ok',
    )
  })
})
