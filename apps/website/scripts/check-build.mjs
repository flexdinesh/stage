// Checks the static build: every route rendered to HTML, no Ask AI anywhere,
// and collapsible docs sidebar sections.
import { readFile } from 'node:fs/promises'

const out = new URL('../dist/public/', import.meta.url)
const routes = [
  'index.html',
  'docs/index.html',
  'docs/concepts/index.html',
  ...['projects', 'connections', 'tasks', 'trees', 'journals', 'schedules', 'sessions', 'machines'].map(
    (p) => `docs/concepts/${p}/index.html`,
  ),
  ...['desktop', 'server', 'workers'].map((p) => `docs/usage/${p}/index.html`),
  ...['first-tree', 'agents', 'phone', 'claude-code'].map((p) => `docs/guides/${p}/index.html`),
  ...['install', 'connections', 'configuration', 'security', 'app', 'cli', 'mcp'].map(
    (p) => `docs/reference/${p}/index.html`,
  ),
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
  if (route.startsWith('docs/') && (html.match(/data-collapsable="true"/g) ?? []).length < 4)
    failures.push(`${route}: sidebar sections not collapsible`)
}

if (failures.length > 0) {
  console.error(failures.join('\n'))
  process.exit(1)
}
console.log(`build ok: ${routes.length} routes static, no Ask AI, collapsible sidebar`)
