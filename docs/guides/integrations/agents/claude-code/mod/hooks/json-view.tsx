import type { EngineInterface, RenderElement, RenderInput } from 'claude-code'

import type { JsonView } from '../types'
import { childPath, headerOf, leafOf, structuredOf, summaryOf, textsOf } from './json'
import type { Leaf, Structured } from './json'
import { fromPromise, isRecord } from './result'
import type { On } from './ui'

const MAX_ROWS = 1500
const MAX_CHILDREN = 200

const LEAF_COLOR: Record<Leaf['type'], string> = {
  string: 'green',
  number: 'yellow',
  boolean: 'magenta',
  null: 'gray',
}

type Els = ReturnType<EngineInterface['ui']['resolve']>

async function edit($: EngineInterface, id: string, change: (v: JsonView) => JsonView): Promise<void> {
  const held = await fromPromise($.state.get({ plugin: 'magus', key: 'json', id }))
  if (held.error) return $.ui.toast(held.error.message)
  const next = change(held.value.value ?? { open: [], isRaw: false })
  const written = await fromPromise($.state.set({ plugin: 'magus', key: 'json', id }, next))
  if (written.error) $.ui.toast(written.error.message)
}

function flip(open: string[], path: string): string[] {
  return open.includes(path) ? open.filter(p => p !== path) : [...open, path]
}

// Rows is the tree flattened to one line per node, drawn until MAX_ROWS so a
// fully opened large record still draws in bounded time.
class Rows {
  readonly out: RenderElement[] = []
  cut = 0

  constructor(
    private readonly els: Els,
    private readonly open: Set<string>,
    private readonly flip: (path: string) => void,
  ) {}

  private push(row: RenderElement): void {
    if (this.out.length < MAX_ROWS) this.out.push(row)
    else this.cut++
  }

  private line(path: string, depth: number, parts: RenderElement[], canOpen: boolean, tail = ''): void {
    const { Box, Button, Text } = this.els
    const isOpen = this.open.has(path)
    this.push(
      <Box key={`row${path}`} paddingLeft={depth * 2} gap={1}>
        {canOpen ? (
          <Button key={`json${path}`} plain dimColor label={isOpen ? '▾' : '▸'} onPress={() => this.flip(path)} />
        ) : (
          <Text> </Text>
        )}
        <Text wrap="wrap">
          {parts}
          {tail !== '' ? <Text dimColor>{tail}</Text> : null}
        </Text>
      </Box>,
    )
  }

  node(label: string | null, v: unknown, path: string, depth: number, comma: boolean): void {
    const { Text } = this.els
    const key = label === null ? [] : [<Text color="cyan">{JSON.stringify(label)}</Text>, <Text dimColor>: </Text>]
    const sep = comma ? ',' : ''
    if (typeof v !== 'object' || v === null) {
      const leaf = leafOf(v)
      this.line(path, depth, [...key, <Text color={LEAF_COLOR[leaf.type]}>{leaf.text}</Text>], false, sep)
      return
    }
    const header = headerOf(v)
    const named = header === null ? [] : [<Text bold>{header} </Text>]
    if (!this.open.has(path)) {
      this.line(path, depth, [...key, ...named], true, `${summaryOf(v)}${sep}`)
      return
    }
    const isArray = Array.isArray(v)
    this.line(path, depth, [...key, ...named], true, isArray ? '[' : '{')
    const entries: Array<[string | null, unknown, string | number]> = isArray
      ? v.map((c, i) => [null, c, i])
      : Object.entries(v).map(([k, c]) => [k, c, k])
    entries.slice(0, MAX_CHILDREN).forEach(([k, c, seg], i) => {
      this.node(k, c, childPath(path, seg), depth + 1, i < entries.length - 1)
    })
    if (entries.length > MAX_CHILDREN) {
      this.line(`${path}/…`, depth + 1, [], false, `… ${entries.length - MAX_CHILDREN} more; Raw shows all`)
    }
    this.line(`${path}/end`, depth, [], false, `${isArray ? ']' : '}'}${sep}`)
  }
}

function rawOf(s: Structured, text: string): string {
  return s.kind === 'jsonl' ? text.trim() : JSON.stringify(s.value, null, 2)
}

// Found is one drawn result's text read as data. id keys which nodes are open.
export type Found = { id: string; s: Structured; text: string; stderr: string; isErrored: boolean }

// explorer draws structured output as a tree, every node closed until
// pressed, with the record's envelope named on its closed row.
export async function explorer($: EngineInterface, e: RenderInput, f: Found) {
  const els = $.ui.resolve(e)
  const { Box, Button, Code, Text } = els
  const { id, s, text, stderr } = f
  const { value: view = { open: [], isRaw: false } } = await $.state.get({ plugin: 'magus', key: 'json', id })
  const rows = new Rows(els, new Set(view.open), path => {
    void edit($, id, v => ({ ...v, open: flip(v.open, path) }))
  })
  if (s.kind === 'json') rows.node(null, s.value, '', 0, false)
  else s.records.forEach((r, i) => rows.node(null, r, `#${i}`, 0, false))
  const count = s.kind === 'jsonl' ? `${s.records.length} records` : ''
  return (
    <Box flexDirection="column">
      {s.kind === 'json' && s.lead !== '' ? <Text color={f.isErrored ? 'red' : undefined}>{s.lead}</Text> : null}
      <Box gap={1}>
        <Text dimColor>{count !== '' ? `JSON Lines · ${count}` : 'JSON'}</Text>
        <Button
          key="json-raw"
          plain
          dimColor
          label={view.isRaw ? 'Tree' : 'Raw'}
          onPress={() => void edit($, id, v => ({ ...v, isRaw: !v.isRaw }))}
        />
      </Box>
      {view.isRaw ? <Code source={rawOf(s, text)} language="json" /> : rows.out}
      {!view.isRaw && rows.cut > 0 ? <Text dimColor>{rows.cut} more rows; Raw shows all</Text> : null}
      {s.kind === 'json' && s.trail !== '' ? <Text dimColor>{s.trail}</Text> : null}
      {stderr.trim() !== '' ? <Text dimColor>{stderr.trim()}</Text> : null}
    </Box>
  )
}

function found(id: string, output: unknown, isErrored: boolean): Found | null {
  const texts = textsOf(output)
  const s = texts === null ? null : structuredOf(texts.text)
  return texts === null || s === null ? null : { id, s, isErrored, ...texts }
}

// blanked is a call's output with its text emptied, so the engine's row keeps
// its header and draws no raw copy under the tree.
function blanked(output: unknown, f: Found): unknown {
  if (typeof output === 'string') return f.s.kind === 'json' ? f.s.lead : ''
  if (Array.isArray(output)) return []
  if (isRecord(output) && Array.isArray(output.content)) return { ...output, content: [] }
  if (isRecord(output) && typeof output.stdout === 'string') return { ...output, stdout: '', stderr: '' }
  return output
}

// registerJsonExplorer draws every tool result whose text is JSON or JSON
// Lines as a collapsible tree in place of the raw text. The terminal draws a
// call's output in its own result block; desktop, VS Code and mobile draw it
// inside the call's row, so there the row carries the tree.
export function registerJsonExplorer(on: On): void {
  on('ui.render', { component: 'ToolResult' }, async ($, e, next) => {
    if (e.surface !== 'terminal') return next(e)
    const f = found(e.props.tool_use_id, e.props.output, e.props.isErrored)
    return f === null ? next(e) : explorer($, e, f)
  })

  on('ui.render', { component: 'ToolUse' }, async ($, e, next) => {
    if (e.surface === 'terminal' || e.props.isRunning) return next(e)
    const f = found(e.props.tool_use_id, e.props.output, e.props.isErrored)
    if (f === null) return next(e)
    const { Box } = $.ui.resolve(e)
    const own = await next({ ...e, props: { ...e.props, output: blanked(e.props.output, f) } })
    return (
      <Box flexDirection="column">
        {own}
        {await explorer($, e, f)}
      </Box>
    )
  })
}
