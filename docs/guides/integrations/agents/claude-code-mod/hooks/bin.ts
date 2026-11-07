import type { EngineInterface } from 'claude-code'

// magusBin prefers the workspace's own ./magus, the one the guard runs.
export async function magusBin($: EngineInterface, root: string): Promise<string> {
  return (await $.fs.exists(`${root}/magus`)) ? `${root}/magus` : 'magus'
}
