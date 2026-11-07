import type { EngineInterface, RenderElement } from 'claude-code'

import { jsonlPaths, parseJsonl, resolvePath, toMarkdown } from './jsonl'
import type { JsonlTable } from './jsonl'
import { fromPromise, isRecord, type Result } from './result'
import type { On, RowInput } from './ui'

const MAX_ROWS = 200
const MAX_COLUMNS = 20
const MAX_SHOWN_ERRORS = 5
const MAX_BYTES = 2_000_000
const MAX_MEMOS = 32

type Loaded =
  | { kind: 'table'; path: string; table: JsonlTable }
  | { kind: 'missing'; path: string }
  | { kind: 'error'; path: string; error: string }

// A parsed file is kept by path and mtime, so a scroll redraw reads nothing.
const memo = new Map<string, { mtimeMs: number; table: JsonlTable }>()

function remember(path: string, mtimeMs: number, table: JsonlTable): void {
  memo.delete(path)
  memo.set(path, { mtimeMs, table })
  for (const key of memo.keys()) {
    if (memo.size <= MAX_MEMOS) break
    memo.delete(key)
  }
}

// The table draws for the person and reaches no model, so a path outside the
// workspace is read on purpose: the notes folder lives in Application Support.
async function load($: EngineInterface, path: string): Promise<Loaded> {
  const exists = await fromPromise($.fs.exists(path))
  if (exists.error) return { kind: 'error', path, error: exists.error.message }
  // A mention of a file that is not there is common in prose, so it draws
  // dim; any other read failure draws red.
  if (!exists.value) return { kind: 'missing', path }
  const stat = await fromPromise($.fs.stat(path))
  if (stat.error) return { kind: 'error', path, error: stat.error.message }
  if (stat.value.kind !== 'file') return { kind: 'error', path, error: 'not a file' }
  if (stat.value.size > MAX_BYTES) {
    return { kind: 'error', path, error: `${stat.value.size} bytes is over the ${MAX_BYTES} byte limit` }
  }
  const kept = memo.get(path)
  if (kept?.mtimeMs === stat.value.mtimeMs) return { kind: 'table', path, table: kept.table }
  const read = await fromPromise($.fs.read(path))
  if (read.error) return { kind: 'error', path, error: read.error.message }
  const table = parseJsonl(read.value)
  remember(path, stat.value.mtimeMs, table)
  return { kind: 'table', path, table }
}

type Target = { kind: 'path'; path: string } | Extract<Loaded, { kind: 'error' }>

// A read that failed turns only the paths that need it into red lines.
function targetOf(path: string, home: Result<string | undefined>, cwd: Result<string>): Target {
  if (path === '~' || path.startsWith('~/')) {
    if (home.error) return { kind: 'error', path, error: home.error.message }
    if (!home.value) return { kind: 'error', path, error: 'HOME is not set' }
    return { kind: 'path', path: resolvePath(path, home.value, '') }
  }
  if (path.startsWith('/')) return { kind: 'path', path: resolvePath(path, '', '') }
  if (cwd.error) return { kind: 'error', path, error: cwd.error.message }
  return { kind: 'path', path: resolvePath(path, '', cwd.value) }
}

// Two mentions that resolve to one file draw once.
function resolveAll(paths: string[], home: Result<string | undefined>, cwd: Result<string>): Target[] {
  const seen = new Set<string>()
  return paths.map(p => targetOf(p, home, cwd)).filter(t => !seen.has(t.path) && seen.add(t.path))
}

function summary(table: JsonlTable): string {
  const rows = `${table.rows.length} row${table.rows.length === 1 ? '' : 's'}`
  const bad = table.errors.length
  return bad === 0 ? rows : `${rows}, ${bad} line${bad === 1 ? '' : 's'} failed to parse`
}

async function draw($: EngineInterface, e: RowInput, own: RenderElement, paths: string[]): Promise<RenderElement> {
  const { Box, Markdown, Text } = $.ui.resolve(e)
  const loaded = await loadAll($, paths)
  return (
    <Box flexDirection="column">
      {own}
      {loaded.map(l => {
        if (l.kind === 'missing') {
          return (
            <Text key={`jsonl-${l.path}`} dimColor>
              ○ {l.path}: no such file
            </Text>
          )
        }
        if (l.kind === 'error') {
          return (
            <Text key={`jsonl-${l.path}`} color="red">
              ✖ {l.path}: {l.error}
            </Text>
          )
        }
        const { table } = l
        const unshown = table.errors.length - MAX_SHOWN_ERRORS
        return (
          <Box key={`jsonl-${l.path}`} flexDirection="column" marginTop={1}>
            <Text bold>
              {l.path} · {summary(table)}
            </Text>
            {table.errors.slice(0, MAX_SHOWN_ERRORS).map(err => (
              <Text key={`err-${err.line}`} color="red">
                ✖ line {err.line}: {err.message}
              </Text>
            ))}
            {unshown > 0 ? <Text color="red">✖ {unshown} more lines failed to parse</Text> : null}
            {table.columns.length > 0 ? <Markdown text={toMarkdown(table, MAX_ROWS, MAX_COLUMNS)} /> : null}
          </Box>
        )
      })}
    </Box>
  )
}

async function loadAll($: EngineInterface, paths: string[]): Promise<Loaded[]> {
  const home = await fromPromise($.env.get('HOME'))
  const cwd = await fromPromise($.session.root())
  const resolved = resolveAll(paths, home, cwd)
  return Promise.all(resolved.map(t => (t.kind === 'path' ? load($, t.path) : t)))
}

function sentFiles(input: unknown): string[] {
  if (!isRecord(input) || !Array.isArray(input.files)) return []
  return input.files.filter((f): f is string => typeof f === 'string' && f.endsWith('.jsonl'))
}

// registerJsonlTables draws each .jsonl file a reply names, or SendUserFile
// sends, as a table under the engine's own row.
export function registerJsonlTables(on: On): void {
  on('ui.render', { component: 'AssistantMessage' }, async ($, e, next) => {
    const own = await next(e)
    const paths = jsonlPaths(e.props.text)
    return paths.length === 0 ? own : draw($, e, own, paths)
  })

  on('ui.render', { component: 'ToolUse' }, async ($, e, next) => {
    const own = await next(e)
    if (e.props.tool !== 'SendUserFile' || e.props.isRunning || e.props.isErrored) return own
    const paths = sentFiles(e.props.input)
    return paths.length === 0 ? own : draw($, e, own, paths)
  })
}
