import type { EngineInterface, Register, RenderElement, RenderInput } from 'claude-code'

import { jsonlPaths, parseJsonl, resolvePath, toMarkdown } from './jsonl'
import type { JsonlTable } from './jsonl'
import { fromPromise } from './result'

type On = Parameters<Register>[0]

const MAX_ROWS = 200

type Loaded = { path: string; table: JsonlTable } | { path: string; missing: true } | { path: string; error: string }

async function load($: EngineInterface, path: string): Promise<Loaded> {
  // A mention of a file that is not there is common in prose, so it draws
  // dim; any other read failure draws red.
  if (!(await $.fs.exists(path))) return { path, missing: true }
  const read = await fromPromise($.fs.read(path))
  if (read.error) return { path, error: read.error.message }
  return { path, table: parseJsonl(read.value) }
}

async function absolute($: EngineInterface, paths: string[]): Promise<string[]> {
  const home = (await $.env.get('HOME')) ?? ''
  const cwd = await $.session.root()
  return paths.map(p => resolvePath(p, home, cwd))
}

function summary(table: JsonlTable): string {
  const rows = `${table.rows.length} row${table.rows.length === 1 ? '' : 's'}`
  const bad = table.errors.length
  return bad === 0 ? rows : `${rows}, ${bad} line${bad === 1 ? '' : 's'} failed to parse`
}

type TranscriptInput = RenderInput<'AssistantMessage'> | RenderInput<'ToolUse'>

async function draw($: EngineInterface, e: TranscriptInput, own: RenderElement, paths: string[]): Promise<RenderElement> {
  const loaded = await Promise.all((await absolute($, paths)).map(p => load($, p)))
  const { Box, Markdown, Text } = $.ui.resolve(e)
  return (
    <Box flexDirection="column">
      {own}
      {loaded.map(l => {
        if ('missing' in l) {
          return (
            <Text key={`jsonl-${l.path}`} dimColor>
              ○ {l.path}: no such file
            </Text>
          )
        }
        if ('error' in l) {
          return (
            <Text key={`jsonl-${l.path}`} color="red">
              ✖ {l.path}: {l.error}
            </Text>
          )
        }
        const { table } = l
        return (
          <Box key={`jsonl-${l.path}`} flexDirection="column" marginTop={1}>
            <Text bold>
              {l.path} · {summary(table)}
            </Text>
            {table.errors.map(err => (
              <Text key={`err-${err.line}`} color="red">
                ✖ line {err.line}: {err.message}
              </Text>
            ))}
            {table.columns.length > 0 ? <Markdown text={toMarkdown(table, MAX_ROWS)} /> : null}
          </Box>
        )
      })}
    </Box>
  )
}

function sentFiles(input: unknown): string[] {
  if (typeof input !== 'object' || input === null || !('files' in input)) return []
  const { files } = input
  if (!Array.isArray(files)) return []
  return files.filter((f): f is string => typeof f === 'string' && f.endsWith('.jsonl'))
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
