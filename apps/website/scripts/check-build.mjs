// Checks the static build: every route rendered to HTML, and no Ask AI anywhere.
import { readFile } from 'node:fs/promises'

const out = new URL('../dist/public/', import.meta.url)
const routes = [
  'index.html',
  'docs/index.html',
  'docs/deploy/index.html',
  'docs/features/index.html',
  'docs/agents/index.html',
  'docs/api/index.html',
  'docs/api/projects/index.html',
  'docs/api/tasks/index.html',
  'docs/api/schedules/index.html',
  'docs/api/threads/index.html',
  'docs/api/notifications/index.html',
  'docs/api/other/index.html',
]

const failures = []
for (const route of routes) {
  let html
  try {
    html = await readFile(new URL(route, out), 'utf8')
  } catch {
    failures.push(`${route}: not rendered`)
    continue
  }
  if (html.includes('data-v-ask-ai-container')) failures.push(`${route}: Ask AI rendered`)
}

if (failures.length > 0) {
  console.error(failures.join('\n'))
  process.exit(1)
}
console.log(`build ok: ${routes.length} routes static, no Ask AI`)
