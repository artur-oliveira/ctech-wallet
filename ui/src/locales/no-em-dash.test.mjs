import assert from 'node:assert/strict'
import {readdir, readFile} from 'node:fs/promises'
import test from 'node:test'

const EM_DASH = '—'

function collect(value, path, out) {
  if (typeof value === 'string') {
    if (value.includes(EM_DASH)) out.push(path)
  } else if (value && typeof value === 'object') {
    for (const [k, v] of Object.entries(value)) collect(v, path ? `${path}.${k}` : k, out)
  }
  return out
}

for (const file of ['pt-BR', 'en']) {
  test(`${file} locale has no em dash`, async () => {
    const json = JSON.parse(await readFile(new URL(`./${file}.json`, import.meta.url), 'utf8'))
    assert.deepEqual(collect(json, '', []), [])
  })
}

test('rendered JSX strings and string literals contain no em dash', async () => {
  const root = new URL('../', import.meta.url)
  const offenders = []
  async function walk(dir) {
    for (const entry of await readdir(dir, {withFileTypes: true})) {
      const url = new URL(entry.name + (entry.isDirectory() ? '/' : ''), dir)
      if (entry.isDirectory()) await walk(url)
      else if (/\.(tsx|ts)$/.test(entry.name) && !/\.test\./.test(entry.name)) {
        const lines = (await readFile(url, 'utf8')).split('\n')
        lines.forEach((line, i) => {
          if (!line.includes(EM_DASH)) return
          const t = line.trim()
          // Code comments are out of scope: line, block, JSDoc and JSX comments.
          if (t.startsWith('//') || t.startsWith('*') || t.startsWith('/*') || t.startsWith('{/*')) return
          offenders.push(`${url.pathname.split('/src/')[1]}:${i + 1}`)
        })
      }
    }
  }
  await walk(root)
  assert.deepEqual(offenders, [])
})
