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
      {
        text: 'Guide',
        items: [
          { text: 'Introduction', link: '/docs' },
          { text: 'Install', link: '/docs/install' },
          { text: 'Connect to T3', link: '/docs/t3' },
          { text: 'Desktop app', link: '/docs/desktop' },
          { text: 'Headless server', link: '/docs/deploy' },
          { text: 'Features', link: '/docs/features' },
          { text: 'Agents', link: '/docs/agents' },
        ],
      },
    ],
  },
})
