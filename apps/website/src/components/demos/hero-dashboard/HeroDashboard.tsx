import { Bar, Button, Collapse, Count, Fly, Kbd, Phone, Roll, Stage, Window, type Act, type Layout, type Rect } from '../../../vendor/stageshow'
import { StageMark } from '../../site/StageMark'
import { useSiteTheme } from '../shared/useSiteTheme'
import { IconCheck, IconChevronDown, IconPlus, IconSearch, IconThemeSystem } from './icons'
import './hero-dashboard.css'

// The landing hero: stage's Dashboard in a browser window, at laptop size.
// Beats: a Running task asks a question, so it lifts out of Running into
// Needs input (the empty Needs input line becomes its panel, Running closes
// the gap); counts roll and the Dashboard tab takes its count chip. The
// cursor presses the row's Answer, stage's Needs input form opens inline,
// an option is picked and Send answer pressed: the form folds, the row goes
// back to Running, yellow leaves and the counts roll back. That return is
// the loop's starting frame, so the seam is the answer being sent.
//
// Real strings: the chrome (mark, tabs, New task), the H1 and its meta line,
// the stat strip, the panel labels, the focal task and its question. Every
// other row keeps its state chip (stage's signature mark) and bars.

type Where = 'run' | 'need'

type S = {
  /** The panel holding the focal row. */
  where: Where
  /** The row is in the air between panels; both real copies hide. */
  flying: boolean
  /** The flyer's look; it changes during the last part of the flight. */
  look: Where
  from: Rect
  to: Rect
  /** Counts and the tab chip follow the row once it has landed. */
  counted: boolean
  form: boolean
  picked: boolean
  sending: boolean
  /** The answer is in: Send answer confirms on itself and the row reads running again. */
  sent: boolean
}

const ZERO: Rect = { x: 0, y: 0, width: 0, height: 0 }

const initial: S = { where: 'run', flying: false, look: 'run', from: ZERO, to: ZERO, counted: false, form: false, picked: false, sending: false, sent: false }

/** The payoff: the answer is sent, the button confirms it, the task reads running again. */
const POSTER: S = { ...initial, where: 'need', look: 'run', counted: true, form: true, picked: true, sent: true }

/** The Needs input header in panel mode: 32px bar plus its rule. */
const HEAD = 33
const FLIGHT = 700

/** The full hero column (1240 at 1440 and up), so the frame's edges are the column's. */
const WIDE = { width: 1240, height: 682 }
const COMPACT_H = 660

const REST = { wide: { x: 700, y: 158 }, compact: { x: 250, y: 200 } }

/** The compact scene is laid out in the phone's 402pt screen, scaled into the stage. */
const SCREEN_PT = 402

/** A stage rect in the scene's own (unscaled) coordinates, where the flyer is drawn. */
function local(a: Act<S>, r: Rect): Rect {
  const sc = a.rect('screen')
  const k = a.layout === 'compact' ? sc.width / SCREEN_PT : 1
  return { x: (r.x - sc.x) / k, y: (r.y - sc.y) / k, width: r.width / k, height: r.height / k }
}

async function script(a: Act<S>) {
  // Where the row sits at rest: beat 3 lands it back here
  const home = local(a, a.rect('row-run'))
  await a.wait(1200)

  // 1. The task asks: it lifts out of Running into Needs input
  a.mark('flip')
  // The landing slot, measured as it will be once the empty line is a 33px header
  const slot = local(a, a.rect('row-need'))
  const head = local(a, a.rect('needs-head'))
  const to: Rect = { x: home.x, y: slot.y - (head.height - HEAD), width: home.width, height: home.height }
  await a.set({ from: home, to, flying: true, look: 'run' })
  a.put({ where: 'need' })
  await a.wait(FLIGHT * 0.2)
  a.put({ look: 'need' })
  // Held a beat past landing, so the chip has finished rolling at the handoff
  await a.wait(FLIGHT * 0.8 + 320)
  await a.set({ flying: false, counted: true })
  await a.wait(1300)

  // 2. Answer opens stage's Needs input form under the row
  a.mark('answer')
  await a.move({ at: 'answer', x: 0.9, y: 0.82 })
  await a.wait(150)
  await a.click()
  await a.set({ form: true })
  await a.wait(1400)

  // 3. Pick an option, send it
  a.mark('pick')
  await a.move({ at: 'opt-0', x: a.layout === 'compact' ? 0.82 : 0.5, y: 0.6 })
  await a.wait(120)
  await a.click()
  await a.set({ picked: true })
  await a.wait(650)
  await a.move({ at: 'send', x: 0.78, y: 0.62 })
  await a.wait(120)
  await a.click()
  await a.set({ sending: true })
  await a.wait(650)

  // The answer is in: the button confirms on itself, the row reads running again
  a.mark('sent')
  await a.set({ sending: false, sent: true })
  await a.wait(1100)
  await a.set({ form: false })
  void a.move(a.layout === 'compact' ? REST.compact : REST.wide, { duration: 900 })
  await a.wait(720)

  // The row goes back to Running; Needs input folds back to its empty line
  const back = local(a, a.rect('row-need'))
  await a.set({ from: back, to: home, flying: true, look: 'run' })
  a.put({ where: 'run' })
  await a.wait(FLIGHT + 320)
  await a.set({ flying: false, counted: false, picked: false, sent: false })
  await a.wait(1900)
  a.put({ from: ZERO, to: ZERO })
}

export function HeroDashboard() {
  const theme = useSiteTheme()
  return (
    <Stage<S>
      width={WIDE.width}
      height={WIDE.height}
      compact={{ below: 640, width: 358, height: COMPACT_H }}
      theme={theme}
      initial={initial}
      poster={POSTER}
      script={script}
      cursor={{ at: REST }}
      className="hdb"
    >
      {(s, _rect, layout) => <Scene key={layout} s={s} layout={layout} dark={theme === 'dark'} />}
    </Stage>
  )
}

function Scene({ s, layout, dark }: { s: S; layout: Layout; dark: boolean }) {
  const compact = layout === 'compact'
  const need = s.counted ? 1 : 0
  const run = s.counted ? 2 : 3
  const screen = (
    <div className="hdb-screen" data-target="screen">
      {compact ? <PhoneBar need={need} /> : <TopBar need={need} />}
      <div className="hdb-page">
        <div className="hdb-head">
          <div>
            <div className="hdb-h1">Dashboard</div>
            <div className="hdb-meta">
              <b>{'//'}</b> 4 projects · 5 active
            </div>
          </div>
          <span className="hdb-select">
            All projects
            <IconChevronDown size={12} />
          </span>
        </div>
          <Stats need={need} run={run} compact={compact} />
          <div className="hdb-panels">
            <NeedsBlock s={s} need={need} compact={compact} />
            <section className="hdb-panel">
              <PanelHead tone="run" title="Running" count={run} />
              <ul className="hdb-rows">
                <Collapse open={s.where === 'run'}>
                  <li className="hdb-li">
                    <Row look="run" compact={compact} target="row-run" hidden={s.flying} />
                  </li>
                </Collapse>
                <Filler tone="run" label="running" w={[96, 228, 112]} compact={compact} />
                <Filler tone="run" label="running" w={[72, 176, 132]} compact={compact} />
              </ul>
            </section>
            <section className="hdb-panel">
              <PanelHead tone="done" title="Done · awaiting settle in T3" count={3} />
              <ul className="hdb-rows">
                <Filler tone="done" label="done" w={[84, 204, 120]} compact={compact} />
                <Filler tone="done" label="done" w={[104, 150, 96]} compact={compact} />
                <Filler tone="done" label="done" w={[92, 196, 88]} compact={compact} />
              </ul>
            </section>

          </div>
      </div>
      <Fly from={s.from} to={s.to} go={s.flying} duration={FLIGHT} className="hdb-fly">
        <Row look={s.look} compact={compact} />
      </Fly>
    </div>
  )
  return compact ? (
    <Phone width={326} className="hdb-phone" wallpaper="var(--st-bg)" dark={dark}>
      {screen}
    </Phone>
  ) : (
    <Window url="stage.local" className="hdb-win" body={{ className: 'hdb-body', 'data-crop': true }}>
      {screen}
    </Window>
  )
}

function TopBar({ need }: { need: number }) {
  return (
    <header className="hdb-top">
      <span className="hdb-mark">
        <StageMark needsInput={need > 0} />
        stage
      </span>
      <nav className="hdb-nav">
        {['Dashboard', 'Projects', 'Sessions', 'Schedules', 'History', 'Snippets', 'Settings'].map((t, i) => (
          <span key={t} className="hdb-tab" data-active={i === 0 || undefined} data-need={(i === 0 && need > 0) || undefined}>
            {t}
            {i === 0 ? (
              <span className="hdb-tabchip" data-on={need > 0 || undefined}>
                <span className="hdb-tabchip__in">
                  <span className="hdb-chip hdb-chip--count" data-tone="need">
                    1
                  </span>
                </span>
              </span>
            ) : null}
          </span>
        ))}
      </nav>
      <span className="hdb-tools">
        <span className="hdb-search">
          <IconSearch size={14} />
          <span>Search tasks, threads</span>
          <Kbd keys={['Ctrl K']} className="hdb-kbd" />
        </span>
        <Button className="hdb-sq hdb-ok" icon={<IconCheck />} aria-label="T3 connected" />
        <Button className="hdb-sq" icon={<IconThemeSystem />} aria-label="Theme" />
        <Button variant="primary" className="hdb-btn" icon={<IconPlus size={14} />}>
          New task
        </Button>
      </span>
    </header>
  )
}

function PhoneBar({ need }: { need: number }) {
  return (
    <header className="hdb-top hdb-top--phone">
      <span className="hdb-mark">
        <StageMark needsInput={need > 0} />
        stage
      </span>
      <span className="hdb-tools">
        <Button className="hdb-sq hdb-ok" icon={<IconCheck />} aria-label="T3 connected" />
        <Button className="hdb-sq" icon={<IconSearch />} aria-label="Search" />
        <Button variant="primary" className="hdb-btn" icon={<IconPlus size={14} />}>
          Task
        </Button>
      </span>
    </header>
  )
}

type Tone = 'need' | 'run' | 'done' | 'bad' | 'idle' | 'ok'

const STATS: { tone: Tone; label: string; n?: number }[] = [
  { tone: 'need', label: 'Needs input' },
  { tone: 'run', label: 'Running' },
  { tone: 'done', label: 'Awaiting settle', n: 3 },
  { tone: 'bad', label: 'Failed', n: 0 },
  { tone: 'idle', label: 'Backlog', n: 4 },
  { tone: 'ok', label: 'Settled', n: 12 },
]

function Stats({ need, run, compact }: { need: number; run: number; compact: boolean }) {
  return (
    <div className="hdb-stats">
      {STATS.map((c) => {
        const n = c.tone === 'need' ? need : c.tone === 'run' ? run : (c.n ?? 0)
        return (
          <span key={c.tone} className="hdb-stat" data-tone={c.tone}>
            <span className="hdb-stat__n" data-zero={n === 0 || undefined}>
              <span className="hdb-mk" data-on={n > 0 || undefined} />
              <Count value={n} duration={520} />
            </span>
            {compact ? null : <span className="hdb-caps">{c.label}</span>}
          </span>
        )
      })}
      {compact ? null : <span className="hdb-hint">click a count to filter</span>}
    </div>
  )
}

function PanelHead({ tone, title, count }: { tone: Tone; title: string; count: number }) {
  return (
    <div className="hdb-ph">
      <span className="hdb-label" data-tone={tone}>
        <i className="hdb-sqmk" />
        {title}
      </span>
      <span className="hdb-pmeta">
        <Count value={count} duration={520} />
      </span>
    </div>
  )
}

/** Needs input: the empty ruled line while nothing waits, its panel once a row lands. */
function NeedsBlock({ s, need, compact }: { s: S; need: number; compact: boolean }) {
  const panel = s.where === 'need'
  const empty = 'Nothing waiting on you. Tasks and threads that ask a question land here.'
  return (
    <section className="hdb-panel hdb-nb" data-target="needs-block" data-panel={panel || undefined}>
      <div className="hdb-nh" data-target="needs-head">
        <span className="hdb-nh__line">
          <span className="hdb-label" data-tone="need">
            <i className="hdb-sqmk" />
            Needs input
          </span>
          <span className="hdb-pmeta">
            <Count value={need} duration={520} />
          </span>
          {compact ? (
            <Collapse open={!panel} className="hdb-empty-wrap">
              <span className="hdb-empty">{empty}</span>
            </Collapse>
          ) : (
            <span className="hdb-empty">{empty}</span>
          )}
        </span>
      </div>
      <ul className="hdb-rows">
        <Collapse open={panel}>
          <li className="hdb-li">
            <Row look={s.sent ? 'run' : 'need'} compact={compact} target="row-need" hidden={s.flying} />
            <Collapse open={s.form}>
              <AnswerForm s={s} compact={compact} />
            </Collapse>
          </li>
        </Collapse>
      </ul>
    </section>
  )
}

function Chip({ tone, children }: { tone: Tone; children: string }) {
  return (
    <span className="hdb-chip hdb-chip--dot" data-tone={tone}>
      <Roll>{children}</Roll>
    </span>
  )
}

/** The focal task row, as TaskRow draws it with the Dashboard's project column. */
function Row({ look, compact, target, hidden = false }: { look: Where; compact: boolean; target?: string; hidden?: boolean }) {
  const need = look === 'need'
  return (
    <div className="hdb-row" data-look={look} data-target={target} style={hidden ? { visibility: 'hidden' } : undefined}>
      <span className="hdb-row__state">
        <Chip tone={need ? 'need' : 'run'}>{need ? 'needs input' : 'running'}</Chip>
      </span>
      {compact ? null : <span className="hdb-row__project">billing-api</span>}
      <span className="hdb-row__title">
        {compact ? <span className="hdb-row__project">billing-api</span> : null}
        <span className="hdb-row__line">
          <span className="hdb-num">#51</span>
          <span className="hdb-title">Add retry backoff to webhook sender</span>
        </span>
        <span className="hdb-row__meta">
          <span>claude-opus-5-5</span>
          <span>14m ago</span>
        </span>
      </span>
      <span className="hdb-row__actions">
        <span className="hdb-reveal" data-on={need || undefined}>
          <span className="hdb-reveal__in">
            <Button variant="primary" size="small" className="hdb-btn hdb-btn--sm" data-target={target === 'row-need' ? 'answer' : undefined}>
              Answer
            </Button>
          </span>
        </span>
        <Button size="small" className="hdb-btn hdb-btn--sm">
          Open in app
        </Button>
      </span>
    </div>
  )
}

function Filler({ tone, label, w, compact }: { tone: Tone; label: string; w: [number, number, number]; compact: boolean }) {
  const [project, title, meta] = w
  return (
    <li className="hdb-li">
      <div className="hdb-row hdb-row--filler">
        <span className="hdb-row__state">
          <Chip tone={tone}>{label}</Chip>
        </span>
        {compact ? null : (
          <span className="hdb-row__project">
            <Bar w={project} h={6} tone={3} />
          </span>
        )}
        <span className="hdb-row__title">
          {compact ? (
            <span className="hdb-row__project">
              <Bar w={project} h={6} tone={3} />
            </span>
          ) : null}
          <span className="hdb-row__line">
            <Bar w={compact ? Math.min(title, 180) : title} h={7} tone={3} />
          </span>
          <span className="hdb-row__meta">
            <Bar w={meta} h={5} tone={2} />
          </span>
        </span>
      </div>
    </li>
  )
}

const OPTIONS = ['5xx and timeouts only', 'Every non-2xx response']

/** stage's Needs input form (NeedsInputPanel's RequestForm): one single-select question. */
function AnswerForm({ s, compact }: { s: S; compact: boolean }) {
  return (
    <div className="hdb-ans" data-sent={s.sent || undefined}>
      <div className="hdb-form">
        <div className="hdb-q">
          <div className="hdb-q__text">{compact ? 'Which failures should retry?' : 'Which failures should the webhook sender retry?'}</div>
          <ul className="hdb-opts">
            {OPTIONS.map((o, i) => (
              <li key={o} className="hdb-opt" data-on={(i === 0 && s.picked) || undefined} data-target={`opt-${i}`}>
                <span className="hdb-radio" />
                <span className="hdb-opt__label">{o}</span>
              </li>
            ))}
          </ul>
        </div>
        <div className="hdb-foot">
          <span className="hdb-answered">
            <Count value={s.picked ? 1 : 0} duration={420} />
            /1 answered
          </span>
          <Button
            variant="primary"
            className="hdb-btn hdb-send"
            data-target="send"
            data-disabled={!s.picked || s.sending || undefined}
            icon={s.sending ? <i className="hdb-spin" /> : s.sent ? <IconCheck size={14} /> : undefined}
          >
            {s.sent ? 'Sent' : 'Send answer'}
          </Button>
        </div>
      </div>
    </div>
  )
}
