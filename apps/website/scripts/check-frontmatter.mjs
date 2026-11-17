// Fails when any page under src/pages lacks `showAskAi: false` in its frontmatter.
// vocs has no site-wide switch for Ask AI, so every page must opt out.
import { readdir, readFile } from 'node:fs/promises'
import { join } from 'node:path'

const root = new URL('../src/pages/', import.meta.url).pathname

async function* pages(dir) {
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) yield* pages(path)
    else if (/\.mdx?$/.test(entry.name)) yield path
  }
}

const missing = []
for await (const path of pages(root)) {
  const text = await readFile(path, 'utf8')
  const frontmatter = /^---\n([\s\S]*?)\n---/.exec(text)?.[1] ?? ''
  if (!/^showAskAi:\s*false\s*$/m.test(frontmatter)) missing.push(path.slice(root.length))
}

if (missing.length > 0) {
  console.error(`showAskAi: false missing in frontmatter:\n  ${missing.join('\n  ')}`)
  process.exit(1)
}
console.log('frontmatter ok: Ask AI disabled on every page')
