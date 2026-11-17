'use client'
import { StageMark } from '../../site/StageMark'
import { Button, Collapse, Count, Field, Kbd, Popover, Roll, Stage, Text, type Act } from '../../../vendor/stageshow'
import { Window } from '../../../vendor/stageshow'
import { useSiteTheme } from '../shared/useSiteTheme'
import { IconChevron, IconClose, IconPlus } from './icons'
import './backlog.css'

// stage's New task drawer saving to the backlog. Beats: the prompt is typed,
// then the title; a model is picked (its effort follows); Workspace goes to
// worktree; Start goes to Backlog and the drawer's one primary button rolls
// from "Save & start" to "Save to backlog"; it is pressed, the drawer closes
// and the task lands at the top of the project's backlog, carrying the rows
// below it down. The reset has a cause: the new row's Start sends it on (it
// leaves the backlog), then New task opens a fresh drawer.
//
// Real strings: the drawer's title, labels and typed text, the picked model,
// the button, the new row and two rows that were already waiting.

const TITLE = 'Add rate limit headers'
const PROMPT = 'Return X-RateLimit-Limit, -Remaining and -Reset on every /v1 response. Reuse the token bucket in middleware/limit.ts.'

const MODELS = ['claude-opus-5-5', 'claude-sonnet-5-5', 'claude-haiku-5-5']
const EFFORT_DEFAULT = ['high', 'medium', 'low']
const WORKSPACES = ['root', 'worktree']
const STARTS = ['Now', 'Backlog', 'At', 'After', 'Repeat']

type Focus = 'none' | 'title' | 'prompt'
type S = {
  open: boolean
  focus: Focus
  title: string
  prompt: string
  menu: boolean
  model: number
  ws: number
  start: number
  landed: boolean
  fresh: boolean
  started: boolean
}

const initial: S = { open: true, focus: 'none', title: '', prompt: '', menu: false, model: 1, ws: 0, start: 0, landed: false, fresh: false, started: false }
// The payoff: the drawer has closed and the new row sits on top, still lit as just arrived
const poster: S = { ...initial, open: false, title: TITLE, prompt: PROMPT, model: 0, ws: 1, start: 1, landed: true, fresh: true }

const REST = { wide: { x: 150, y: 440 }, compact: { x: 290, y: 400 } }

async function script(a: Act<S>) {
  const rest = a.layout === 'compact' ? REST.compact : REST.wide
  await a.wait(500)
  a.mark('prompt')
  await a.click({ at: 'prompt', x: 0.78, y: 0.72 })
  a.put({ focus: 'prompt' })
  await a.type('prompt', PROMPT, { interval: 14 })
  await a.wait(200)
  await a.click({ at: 'title', x: 0.86, y: 0.55 })
  a.put({ focus: 'title' })
  await a.type('title', TITLE, { interval: 30 })
  await a.wait(250)
  a.mark('model')
  await a.click({ at: 'model', x: 0.86, y: 0.55 })
  a.put({ focus: 'none', menu: true })
  await a.wait(300)
  await a.click({ at: 'opt-0', x: 0.82, y: 0.55 })
  a.put({ menu: false, model: 0 })
  await a.wait(450)
  await a.click({ at: 'ws-1', x: 0.6, y: 0.62 })
  a.put({ ws: 1 })
  await a.wait(300)
  a.mark('backlog')
  await a.click({ at: 'start-1', x: 0.6, y: 0.62 })
  a.put({ start: 1 })
  await a.wait(650)
  a.mark('save')
  await a.click({ at: 'save', x: 0.5, y: 0.9 })
  a.put({ open: false })
  await a.wait(320)
  a.mark('land')
  a.put({ landed: true, fresh: true })
  await a.wait(1100)
  a.put({ fresh: false })
  await a.wait(1300)
  a.mark('start')
  await a.click({ at: 'row-start', x: 0.5, y: 0.92 })
  a.put({ started: true })
  await a.wait(650)
  a.put({ landed: false })
  await a.wait(450)
  a.mark('new')
  await a.click({ at: 'new', x: 0.62, y: 0.6 })
  // Out of sight behind the closed drawer: the form starts over
  await a.snap({ focus: 'none', title: '', prompt: '', model: 1, ws: 0, start: 0, started: false })
  a.put({ open: true })
  await a.wait(250)
  await a.move(rest)
  await a.wait(300)
}

function Chip({ started }: { started: boolean }) {
  return (
    <span className="bl-chip" data-tone={started ? 'run' : 'idle'}>
      <Roll variant="fade">{started ? 'launching' : 'backlog'}</Roll>
    </span>
  )
}

type RowProps = {
  id: number
  title?: string
  meta?: string
  time?: string
  bars?: [number, number]
  started?: boolean
  fresh?: boolean
  target?: string
}

function TaskRow({ id, title, meta, time, bars, started = false, fresh = false, target }: RowProps) {
  return (
    <div className="bl-row" data-fresh={fresh || undefined}>
      <div className="bl-row__state">
        <Chip started={started} />
      </div>
      <div className="bl-row__main">
        <div className="bl-row__title">
          <span className="bl-row__id">#{id}</span>
          {title !== undefined ? <span className="bl-row__name">{title}</span> : <span className="bl-bar bl-bar--title" style={{ width: bars?.[0] }} />}
        </div>
        <div className="bl-row__meta">
          {meta !== undefined ? (
            <>
              <span>{meta}</span>
              <span>{time}</span>
            </>
          ) : (
            <span className="bl-bar bl-bar--meta" style={{ width: bars?.[1] }} />
          )}
        </div>
      </div>
      <div className="bl-row__actions">
        {bars === undefined ? (
          <Button variant="primary" size="small" className="bl-btn" data-target={target}>
            Start
          </Button>
        ) : (
          <span className="bl-bar bl-bar--action" />
        )}
      </div>
    </div>
  )
}

function Select({ value, target, roll = false }: { value: string; target?: string; roll?: boolean }) {
  return (
    <span className="bl-select" data-target={target} data-cursor={target === undefined ? undefined : 'pointer'}>
      <span className="bl-select__value">{roll ? <Roll variant="fade">{value}</Roll> : value}</span>
      <IconChevron size={14} />
    </span>
  )
}

function Segmented({ options, value, name, fill = false }: { options: string[]; value: number; name: string; fill?: boolean }) {
  return (
    <span className="bl-seg" data-fill={fill || undefined}>
      {options.map((o, i) => (
        <span key={o} className="bl-seg__opt" data-on={i === value || undefined} data-target={`${name}-${i}`} data-cursor="pointer">
          {o}
        </span>
      ))}
    </span>
  )
}

function Drawer({ s, compact }: { s: S; compact: boolean }) {
  return (
    <div className="bl-drawer" data-open={s.open || undefined} aria-hidden={!s.open || undefined}>
      <div className="bl-drawer__head">
        <Text className="bl-drawer__title">New task</Text>
        <div className="bl-drawer__tools">
          {compact ? null : <Kbd keys={['Esc']} className="bl-kbd" />}
          <Button variant="ghost" size="small" className="bl-close" icon={<IconClose size={14} />} aria-label="Close" />
        </div>
      </div>
      <form className="bl-drawer__body" onSubmit={(e) => e.preventDefault()}>
        <Field label="Title" value={s.title} focused={s.focus === 'title'} className="bl-field" box={{ 'data-target': 'title' }} />
        <div className="bl-group">
          <span className="bl-label">Prompt</span>
          <div className="bl-textarea" data-focused={s.focus === 'prompt' || undefined} data-target="prompt" data-cursor="ibeam">
            <span className="bl-textarea__text" data-typing="">
              {s.prompt}
              {s.focus === 'prompt' ? <span key={s.prompt.length} className="bl-caret" /> : null}
            </span>
          </div>
        </div>
        <div className="bl-models">
          {compact ? null : (
            <div className="bl-group">
              <span className="bl-label">Provider</span>
              <Select value="Claude" />
            </div>
          )}
          <div className="bl-group bl-group--menu">
            <span className="bl-label">Model</span>
            <Select value={MODELS[s.model] ?? ''} target="model" roll />
            <Popover show={s.menu} className="bl-menu">
              {MODELS.map((m, i) => (
                <span key={m} className="bl-menu__opt" data-target={`opt-${i}`} data-cursor="pointer" data-current={i === s.model || undefined}>
                  {m}
                </span>
              ))}
            </Popover>
          </div>
          <div className="bl-group">
            <span className="bl-label">Effort</span>
            <Select value={EFFORT_DEFAULT[s.model] ?? ''} roll />
          </div>
        </div>
        <div className="bl-launch">
          <div className="bl-group">
            <span className="bl-label">Workspace</span>
            <Segmented options={WORKSPACES} value={s.ws} name="ws" />
          </div>
          <div className="bl-group bl-group--start">
            <span className="bl-label">Start</span>
            <Segmented options={STARTS} value={s.start} name="start" fill={compact} />
          </div>
        </div>
      </form>
      <div className="bl-drawer__foot">
        <Button variant="primary" size="large" className="bl-btn bl-save" data-target="save">
          {s.start === 1 ? 'Save to backlog' : 'Save & start'}
        </Button>
      </div>
    </div>
  )
}

function Page({ s, compact }: { s: S; compact: boolean }) {
  const count = 5 + (s.landed ? 1 : 0)
  return (
    <div className="bl-page">
      <div className="bl-top">
        <span className="bl-brand">
          <StageMark />
          <span className="bl-brand__name">stage</span>
        </span>
        {compact ? null : (
          <span className="bl-tabs">
            <span className="bl-tab">Stage</span>
            <span className="bl-tab" data-on>
              Projects
            </span>
            <span className="bl-tab">Sessions</span>
            <span className="bl-tab">History</span>
          </span>
        )}
        <Button variant="primary" size={compact ? 'small' : 'medium'} className="bl-btn bl-new" icon={<IconPlus size={compact ? 12 : 14} />} data-target="new">
          New task
        </Button>
      </div>
      <div className="bl-main">
        <div className="bl-head">
          <Text className="bl-h1">api-gateway</Text>
          <div className="bl-meta-line">
            <b>//</b> ~/code/api-gateway
          </div>
        </div>
        <section className="bl-panel">
          <div className="bl-panel__head">
            <span className="bl-panel__title">Tasks</span>
            <Count value={count} className="bl-panel__count" />
            <span className="bl-filter">
              <span>backlog</span>
              <IconChevron size={12} />
            </span>
          </div>
          <div className="bl-rows">
            <Collapse open={s.landed} reveal="clip">
              <TaskRow id={58} title={TITLE} meta="claude-opus-5-5 · high" time="just now" started={s.started} fresh={s.fresh} target="row-start" />
            </Collapse>
            <TaskRow id={57} title="Migrate session store to Redis" meta="claude-sonnet-5-5 · medium" time="2h ago" />
            <TaskRow id={55} title="Fix flaky retry test in webhooks" meta="gpt-6.1-sol · high" time="yesterday" />
            <TaskRow id={54} bars={[compact ? 150 : 210, compact ? 120 : 170]} />
            <TaskRow id={52} bars={[compact ? 120 : 176, compact ? 136 : 190]} />
            <TaskRow id={49} bars={[compact ? 140 : 194, compact ? 110 : 150]} />
          </div>
        </section>
      </div>
      <div className="bl-scrim" data-on={s.open || undefined} />
    </div>
  )
}

export function Demo() {
  const theme = useSiteTheme()
  return (
    <Stage<S>
      width={680}
      height={480}
      compact={{ below: 560, width: 358, height: 600 }}
      initial={initial}
      poster={poster}
      script={script}
      theme={theme}
      cursor={{ at: REST }}
      className="bl"
    >
      {(s, _rect, layout) => {
        const compact = layout === 'compact'
        const screen = (
          <div className="bl-screen" data-crop="" data-drawer={s.open || undefined}>
            <Page s={s} compact={compact} />
            <Drawer s={s} compact={compact} />
          </div>
        )
        // Narrow: stage's phone layout at 1:1 in a hairline viewport (a desktop
        // window this narrow would read as a squashed app)
        return compact ? (
          <div className="bl-viewport">{screen}</div>
        ) : (
          <Window url="stage.local/projects/3" className="bl-win">
            {screen}
          </Window>
        )
      }}
    </Stage>
  )
}
