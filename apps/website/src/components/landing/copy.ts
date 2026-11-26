// All landing copy lives here (facts from stage's docs/*.md contracts, README and PRODUCT.md).
// Demos own their in-UI strings; this file owns the page's words.

export type FeatureId = 'backlog' | 'run-order' | 'schedules' | 'plan-session' | 'pwa-push'

export type Feature = {
  id: FeatureId
  title: string
  body: string
  facts: string[]
  link: { text: string; href: string }
  /** Accessible description of the animated demo (the demo itself is inert). */
  demoLabel: string
}

export const hero = {
  title: 'Manage tasks across T3 Code and OpenCode.',
  body: 'Queue work in either harness, review final replies, and choose when to release the next task. Keep projects, journals and schedules together on your own server.',
  meta: 'desktop or headless · self-hosted',
  primary: { text: 'Get started', href: '/docs' },
  secondary: { text: 'GitHub', href: 'https://github.com/flexdinesh/stage' },
  demoLabel:
    "Animated demo: stage's dashboard, where a running task flips to needs input with a yellow edge, Open in T3 continues the conversation, and the task goes back to running.",
}

export const features: Feature[] = [
  {
    id: 'backlog',
    title: 'Write the prompt now. Start it later.',
    body: 'Save a task with its prompt, provider, model, effort, runtime and workspace. It waits in the backlog until you start it, or until something you queued starts it for you.',
    facts: ['provider, model, effort, worktree', 'prompt images for T3, Claude Code, Codex', 'save to backlog or start right away'],
    link: { text: 'How tasks work', href: '/docs/concepts/tasks' },
    demoLabel: "Animated demo: in stage's New task drawer a prompt is typed, a model and effort are picked, and Save to backlog adds the task to the top of the project's backlog.",
  },
  {
    id: 'run-order',
    title: 'Tasks wait for what they need.',
    body: "Put tasks in a tree, across projects if you need to. A task starts when the tasks it needs are completed in Stage, with each one's accepted result and available references appended to its prompt.",
    facts: ['ready after completion in Stage', 'handoff includes reply, branch and session link'],
    link: { text: 'Trees and handoff', href: '/docs/concepts/trees' },
    demoLabel: "Animated demo: #12 is added as a need of task #13 in its Tree panel; the user completes #12, then task #13 starts automatically with #12's final reply, branch and session link.",
  },
  {
    id: 'schedules',
    title: 'Run it once at nine, or every weekday.',
    body: 'Start a backlog task at a set time, or make a schedule that spawns a fresh task on every fire: hourly, daily, weekdays, weekly or a raw cron, each in its own time zone.',
    facts: ['one-shot or recurring', '5-field cron, IANA time zone', 'skips a fire while the last run is busy'],
    link: { text: 'Scheduled tasks', href: '/docs/concepts/schedules' },
    demoLabel: 'Animated demo: a task is set to repeat on weekdays at 09:00 in its own time zone; when the time comes, the schedule fires and a new task starts running.',
  },
  {
    id: 'plan-session',
    title: 'Plan in one session. Fan out across models.',
    body: 'Let a session plan a complex job and split it into independent pieces. Through stage’s MCP it creates the tasks, puts the ones that depend on each other in a tree, and gives each the model that suits it.',
    facts: ['create_task and create_tree over MCP or CLI', 'run after me waits for completion in Stage', 'task detail shows the batch it created'],
    link: { text: 'Agents, CLI and MCP', href: '/docs/guides/agents' },
    demoLabel: "Animated demo: a T3 Code session plans a migration and calls stage's MCP to create four tasks on different models, groups three into a tree, and starts it.",
  },
  {
    id: 'pwa-push',
    title: 'Know when work needs you.',
    body: 'Install stage as an app and get a push when a session needs input, fails or finishes. Tap it to view the task, then open the session to continue.',
    facts: ['installable PWA with Web Push', 'needs input, failed, ready, done', 'the app badge counts what is waiting'],
    link: { text: 'Install and notifications', href: '/docs/guides/phone' },
    demoLabel: "Animated demo: a stage push notification asks for input on a phone's lock screen; tapping it opens the installed app, Open in T3 continues the conversation.",
  },
]

export const closing = {
  title: 'Connect your harness.',
  body: 'Install the desktop app, or the headless server on a remote machine, then connect your harness and bind a project workspace.',
  primary: { text: 'Install stage', href: '/docs/reference/install' },
  secondary: { text: 'Read the docs', href: '/docs' },
  // Condensed from /docs/reference/install (desktop, then headless).
  // Entries starting with '# ' render as comments.
  quickstart: [
    '# desktop',
    'curl -fsSL https://raw.githubusercontent.com/flexdinesh/stage/main/scripts/install.sh | sh',
    '# CLI/server only',
    'curl -fsSL https://raw.githubusercontent.com/flexdinesh/stage/main/scripts/install.sh | sh -s -- --headless',
  ],
  quickstartNote: 'then connect your harness in Settings',
}
