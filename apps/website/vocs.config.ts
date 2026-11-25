import { defineConfig } from 'vocs/config'

const baseUrl = 'https://stage.hoplabs.dev'

export default defineConfig({
  title: 'stage',
  titleTemplate: '%s | stage',
  description:
    'Task manager for T3 Code. Queue tasks, run them as trees, schedule them, and answer threads that need input from your phone.',
  baseUrl,
  renderStrategy: 'full-static',
  colorScheme: 'light dark',
  accentColor: 'light-dark(#0b0d14, #eef0f5)',
  iconUrl: '/favicon.svg',
  ogImageUrl: `${baseUrl}/og.png`,
  checkDeadlinks: true,
  // No <base href={baseUrl}>: assets must load from the serving origin
  // (Cloudflare preview URLs, local `wrangler dev`).
  head: { base: false },
  mcp: { enabled: false },
  codeHighlight: {
    themes: { light: 'github-light', dark: 'github-dark-dimmed' },
  },
  socials: [{ icon: 'github', link: 'https://github.com/flexdinesh/stage' }],
  editLink: {
    link: 'https://github.com/flexdinesh/stage/edit/main/apps/website/src/pages/:path',
    text: 'Edit this page on GitHub',
  },
  topNav: [
    { text: 'Docs', link: '/docs', match: '/docs' },
    { text: 'GitHub', link: 'https://github.com/flexdinesh/stage' },
  ],
  sidebar: {
    '/docs': [
      { text: 'Overview', link: '/docs' },
      {
        text: 'Concepts',
        link: '/docs/concepts',
        collapsed: false,
        items: [
          { text: 'Projects', link: '/docs/concepts/projects' },
          { text: 'Connections', link: '/docs/concepts/connections' },
          { text: 'Tasks', link: '/docs/concepts/tasks' },
          { text: 'Trees', link: '/docs/concepts/trees' },
          { text: 'Journals', link: '/docs/concepts/journals' },
          { text: 'Schedules', link: '/docs/concepts/schedules' },
          { text: 'Sessions and references', link: '/docs/concepts/sessions' },
          { text: 'Machines', link: '/docs/concepts/machines' },
        ],
      },
      {
        text: 'Usage',
        collapsed: false,
        items: [
          { text: 'Desktop app', link: '/docs/usage/desktop' },
          { text: 'Self-hosted server', link: '/docs/usage/server' },
          { text: 'Multiple machines', link: '/docs/usage/workers' },
        ],
      },
      {
        text: 'Guides',
        collapsed: false,
        items: [
          { text: 'Your first tree', link: '/docs/guides/first-tree' },
          { text: 'Agents that queue work', link: '/docs/guides/agents' },
          { text: 'Answer from your phone', link: '/docs/guides/phone' },
          { text: 'Claude Code', link: '/docs/guides/claude-code' },
        ],
      },
      {
        text: 'Reference',
        collapsed: false,
        items: [
          { text: 'Install and update', link: '/docs/reference/install' },
          { text: 'Connections', link: '/docs/reference/connections' },
          { text: 'Configuration', link: '/docs/reference/configuration' },
          { text: 'Security', link: '/docs/reference/security' },
          { text: 'The app', link: '/docs/reference/app' },
          { text: 'CLI', link: '/docs/reference/cli' },
          { text: 'MCP tools', link: '/docs/reference/mcp' },
        ],
      },
    ],
  },
})
