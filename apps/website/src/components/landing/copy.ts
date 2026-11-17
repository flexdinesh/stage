// All landing copy lives here (drafted from stage's README and PRODUCT.md; pending user review).
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
  title: 'Keep T3 Code busy without babysitting it.',
  body: 'stage is a self-hosted task manager for T3 Code. Save tasks to a backlog, link them to run in order, put them on a schedule, and answer the threads that need you from your phone.',
  meta: 'self-hosted beside T3 · Go + SQLite, one binary · talks to T3 over MCP',
  primary: { text: 'Get started', href: '/docs/deploy' },
  secondary: { text: 'GitHub', href: 'https://github.com/flexdinesh/stage' },
  demoLabel:
    "Animated demo: stage's dashboard, where a running task flips to needs input with a yellow edge, the question is answered in place, and the task goes back to running.",
}

export const features: Feature[] = [
  {
    id: 'backlog',
    title: 'Write the prompt now. Start it later.',
    body: 'Save a task with its prompt, provider, model, effort, runtime and workspace. It waits in the backlog until you start it, or until something you queued starts it for you.',
    facts: ['provider, model, effort, worktree', 'screenshots ride along on every launch', 'save to backlog or start right away'],
    link: { text: 'How tasks work', href: '/docs/features' },
    demoLabel: "Animated demo: in stage's New task drawer a prompt is typed, a model and effort are picked, and Save to backlog adds the task to the top of the project's backlog.",
  },
  {
    id: 'run-order',
    title: 'Link tasks. They run in order.',
    body: "Queue a task to run after another, across projects if you need to. It starts when its predecessor settles in T3, with that thread's final reply, PRs and branch appended to its prompt.",
    facts: ['starts when the predecessor settles', 'handoff: final reply, PRs, branch', "optional: build on the predecessor's branch"],
    link: { text: 'Run order and handoff', href: '/docs/features' },
    demoLabel: "Animated demo: task #13 is linked to run after #12; when #12 settles, #13 starts on its own with #12's final reply, pull request and branch handed off.",
  },
  {
    id: 'schedules',
    title: 'Run it once at nine, or every weekday.',
    body: 'Start a backlog task at a set time, or make a schedule that spawns a fresh task on every fire: hourly, daily, weekdays, weekly or a raw cron, each in its own time zone.',
    facts: ['one-shot or recurring', '5-field cron, IANA time zone', 'skips a fire while the last run is busy'],
    link: { text: 'Scheduled tasks', href: '/docs/features' },
    demoLabel: 'Animated demo: a task is set to repeat on weekdays at 09:00 in its own time zone; when the time comes, the schedule fires and a new task starts running.',
  },
  {
    id: 'plan-session',
    title: 'Plan in one session. Fan out across models.',
    body: 'Let a session plan a complex job and split it into independent pieces. Through stage’s MCP it creates the tasks, links the ones that depend on each other, and gives each the model that suits it.',
    facts: ['create_task and order_tasks over MCP or CLI', 'run after me: start when the planner settles', 'session detail shows the batch it created'],
    link: { text: 'Agents, CLI and MCP', href: '/docs/agents' },
    demoLabel: "Animated demo: a T3 Code session plans a migration and calls stage's MCP to create four tasks on different models, links three into a chain, and starts the chain.",
  },
  {
    id: 'pwa-push',
    title: 'Answer from your lock screen.',
    body: 'Install stage as an app and get a push when a thread needs input, fails or finishes. Tap it, answer the question in place, and the thread carries on.',
    facts: ['installable PWA with Web Push', 'needs input, failed, done, settled', 'the app badge counts what is waiting'],
    link: { text: 'Install and notifications', href: '/docs/deploy' },
    demoLabel: "Animated demo: a stage push notification asks for input on a phone's lock screen; tapping it opens the installed app, an answer is sent, and the task runs again.",
  },
]

export const closing = {
  title: 'Run it next to T3.',
  body: 'Run docker compose or the single binary on the T3 host, point it at T3, and stage imports your projects.',
  primary: { text: 'Deploy stage', href: '/docs/deploy' },
  secondary: { text: 'Read the docs', href: '/docs' },
  // Condensed from /docs/deploy "Run with Docker compose".
  quickstart: [
    'git clone https://github.com/flexdinesh/stage.git && cd stage',
    'cp .env.example .env    # set T3_ADMIN_TOKEN',
    'docker compose up -d --build',
  ],
  quickstartNote: 'open http://127.0.0.1:7380 on the host',
}
