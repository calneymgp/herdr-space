import {existsSync} from 'node:fs'
import path from 'node:path'

export function goCommand(root) {
  if (process.env.HERDR_GO_BINARY) return process.env.HERDR_GO_BINARY
  const local = path.join(root, '.tools/go1.27.1/bin/go')
  return existsSync(local) ? local : 'go'
}
