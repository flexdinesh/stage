import { Bar, Button, Collapse, Phone, Roll, Segmented, Stage, Window, type Act } from '../../../vendor/stageshow'
import { StageMark } from '../../site/StageMark'
import { useSyncExternalStore, type ReactNode } from 'react'
import { useSiteTheme } from '../shared/useSiteTheme'
import { IconCheck, IconChevronDown, IconClose, IconPlus, IconRepeat, IconSearch } from './icons'
import './schedules.css'

// stage's scheduled tasks, as a flow inside the app. The New task drawer is
// open; the pointer picks Start: Repeat (the drawer becomes "New schedule"
// and the primary button "Save schedule"), then the Weekdays preset (the
// summary, cron and next fires roll). Save closes the drawer onto the
// Dashboard, where the schedule opens in "Scheduled · next 24h" with its
// "next in 3s" ticking down. At 09:00 it fires: a fresh task opens in
// Running (launching, then running) carrying the schedule chip, and the
// active count rolls. The pointer opens New task again: the drawer covers the
// dashboard, which resets unseen, and the loop begins where it started.
//
// Real strings: the drawer title, the Start and Repeat options, time, zone,
// summary, cron and next fires, the button, the schedule row and the task it
// spawned. Everything else on the dashboard is filler.

type Mode = 'now' | 'repeat'
type Preset = 'daily' | 'weekdays'
type Run = 'launching' | 'running'
type S = { drawer: boolean; mode: Mode; preset: Preset; saved: boolean; left: number; fired: boolean; run: Run }

// Where the pointer rests: empty drawer surface beside Start
const REST = { wide: { x: 560, y: 150 }, compact: { x: 220, y: 93 } }

const TITLE = 'Triage overnight issues'
const ZONE = 'Asia/Kolkata'

const initial: S = { drawer: true, mode: 'now', preset: 'daily', saved: false, left: 3, fired: false, run: 'launching' }
const poster: S = { drawer: false, mode: 'repeat', preset: 'weekdays', saved: true, left: 0, fired: true, run: 'running' }

const START_WIDE = ['Now', 'Backlog', 'At time', 'After task', 'Repeat']
const START_SHORT = ['Now', 'Backlog', 'At', 'After', 'Repeat']
const PRESET_WIDE = ['Hourly', 'Daily', 'Weekdays', 'Weekly', 'Cron']
const PRESET_SHORT = ['Hourly', 'Daily', 'Mon-Fri', 'Weekly', 'Cron']

// "Now" is Friday Nov 20, 08:59:57 in Kolkata: Daily and Weekdays share the
// first fire, then Weekdays skips the weekend.
const SPEC = {
  daily: { summary: 'every day at 09:00', cron: '0 9 * * *', fires: ['Nov 20 09:00', 'Nov 21 09:00', 'Nov 22 09:00'] },
  weekdays: { summary: 'weekdays at 09:00', cron: '0 9 * * 1-5', fires: ['Nov 20 09:00', 'Nov 23 09:00', 'Nov 24 09:00'] },
}

async function script(a: Act<S>) {
  await a.wait(900)
  a.mark('repeat')
  await a.move({ at: 'start-repeat', x: 0.5, y: 0.82 })
  await a.wait(150)
  await a.click({ at: 'start-repeat', x: 0.5, y: 0.82 })
  await a.set({ mode: 'repeat' })
  await a.wait(900)
  a.mark('weekdays')
  await a.move({ at: 'preset-weekdays', x: 0.5, y: 0.82 })
  await a.wait(150)
  await a.click({ at: 'preset-weekdays', x: 0.5, y: 0.82 })
  await a.set({ preset: 'weekdays' })
  await a.wait(1700)
  a.mark('save')
  await a.move({ at: 'save', x: 0.8, y: 0.7 })
  await a.wait(150)
  await a.click({ at: 'save', x: 0.8, y: 0.7 })
  await a.set({ drawer: false })
  // The schedule opens in its list as the drawer uncovers it
  await a.wait(150)
  await a.set({ saved: true })
  // Off the list, so the row and what it spawns are in clear view
  void a.move('aside', { duration: 700 })
  await a.wait(1000)
  a.mark('tick')
  await a.set({ left: 2 })
  await a.wait(1000)
  await a.set({ left: 1 })
  await a.wait(1000)
  a.mark('fire')
  await a.set({ fired: true })
  await a.wait(1000)
  await a.set({ run: 'running' })
  await a.wait(2400)
  // A new task, from the top bar: the drawer covers the dashboard
  a.mark('again')
  await a.snap({ mode: 'now', preset: 'daily' })
  await a.move({ at: 'new-task', x: 0.7, y: 0.72 })
  await a.wait(150)
  await a.click({ at: 'new-task', x: 0.7, y: 0.72 })
  await a.set({ drawer: true })
  await a.wait(600)
  await a.snap({ saved: false, left: 3, fired: false, run: 'launching' })
  await a.move(a.layout === 'compact' ? REST.compact : REST.wide, { duration: 600 })
  await a.wait(300)
}

function Chip({ tone, dot, children }: { tone: 'run' | 'idle' | 'plain'; dot?: boolean; children: ReactNode }) {
  return (
    <span className="sch-chip" data-tone={tone} data-dot={dot || undefined}>
      {children}
    </span>
  )
}

function Drawer({ s, compact }: { s: S; compact: boolean }) {
  const spec = SPEC[s.preset]
  const repeat = s.mode === 'repeat'
  return (
    <div className="sch-drawer" data-open={s.drawer || undefined} aria-hidden={!s.drawer || undefined}>
      <div className="sch-drawer__head">
        <div className="sch-drawer__title">
          <Roll variant="fade">{repeat ? 'New schedule' : 'New task'}</Roll>
        </div>
        <span className="sch-drawer__end">
          {compact ? null : <span className="sch-kbd">Esc</span>}
          <span className="sch-iconbtn sch-iconbtn--ghost">
            <IconClose size={16} />
          </span>
        </span>
      </div>
      <div className="sch-drawer__body">
        <div className="sch-field">
          <div className="sch-label">Start</div>
          <Segmented
            className="sch-seg"
            items={compact ? START_SHORT : START_WIDE}
            value={repeat ? 4 : 0}
            targets={[undefined, undefined, undefined, undefined, 'start-repeat']}
          />
        </div>
        <Collapse open={repeat}>
          <div className="sch-repeat">
            <div className="sch-field">
              <div className="sch-label">Repeat</div>
              <Segmented
                className="sch-seg"
                items={compact ? PRESET_SHORT : PRESET_WIDE}
                value={s.preset === 'weekdays' ? 2 : 1}
                targets={[undefined, undefined, 'preset-weekdays', undefined, undefined]}
              />
            </div>
            <div className="sch-grid">
              <div className="sch-field">
                <div className="sch-label">Time</div>
                <div className="sch-input sch-mono">09:00</div>
              </div>
              <div className="sch-field">
                <div className="sch-label">Time zone</div>
                <div className="sch-input sch-mono sch-select">
                  {ZONE}
                  <IconChevronDown size={14} />
                </div>
              </div>
            </div>
            <div className="sch-preview">
              <div className="sch-preview__line">
                <span className="sch-fg">
                  <Roll>{spec.summary}</Roll>
                  <span className="sch-muted"> · {ZONE}</span>
                </span>
                {compact ? null : (
                  <span className="sch-cron">
                    <Roll align="end">{spec.cron}</Roll>
                  </span>
                )}
              </div>
              {compact ? (
                // The phone's box wraps the third fire onto its own line
                <>
                  <div className="sch-muted">
                    <Roll>{`next: ${spec.fires[0]} · ${spec.fires[1]} ·`}</Roll>
                  </div>
                  <div className="sch-muted">
                    <Roll>{spec.fires[2]}</Roll>
                  </div>
                </>
              ) : (
                <div className="sch-muted">
                  <Roll>{`next: ${spec.fires.join(' · ')}`}</Roll>
                </div>
              )}
            </div>
          </div>
        </Collapse>
        {/* Below Start the form runs on (notes); the repeat fields take
            their room, as the body would scroll them out of view */}
        <Collapse open={!repeat}>
          <div className="sch-field sch-after">
            <div className="sch-label">Notes</div>
            <div className="sch-input sch-textarea" />
          </div>
        </Collapse>
      </div>
      <div className="sch-drawer__foot">
        <Button variant="primary" size="large" className="sch-primary" data-target="save">
          {repeat ? 'Save schedule' : 'Save & start'}
        </Button>
      </div>
    </div>
  )
}

function TopBar({ compact }: { compact: boolean }) {
  return (
    <div className="sch-top">
      <span className="sch-mark">
        <StageMark />
        stage
      </span>
      {compact ? null : (
        <span className="sch-tabs">
          <span className="sch-tab" data-on>
            Dashboard
          </span>
          <span className="sch-tab">Projects</span>
          <span className="sch-tab">Sessions</span>
          <span className="sch-tab">Schedules</span>
        </span>
      )}
      <span className="sch-top__end">
        <span className="sch-iconbtn">
          <IconSearch size={compact ? 16 : 14} />
        </span>
        <span className="sch-iconbtn sch-iconbtn--ok">
          <IconCheck size={compact ? 16 : 14} />
        </span>
        <span className="sch-newtask" data-target="new-task" data-cursor="pointer">
          <IconPlus size={14} />
          {compact ? <span>Task</span> : null}
        </span>
      </span>
    </div>
  )
}

function Dashboard({ s, compact }: { s: S; compact: boolean }) {
  const next = s.fired ? 'in 3d' : `in ${s.left}s`
  return (
    <div className="sch-app">
      <TopBar compact={compact} />
      <div className="sch-page">
        <div className="sch-head">
          <div>
            <div className="sch-h1">Dashboard</div>
            <div className="sch-pagemeta">
              <b>//</b> 3 projects · <Roll>{s.fired ? '2 active' : '1 active'}</Roll>
            </div>
          </div>
          <span className="sch-aside" data-target="aside" />
        </div>

        <section className="sch-panel">
          <div className="sch-panel__head">
            <span className="sch-panel__title" data-tone="run">
              <span className="sch-sq" />
              Running
            </span>
            <span className="sch-panel__meta">
              <Roll>{s.fired ? '2' : '1'}</Roll>
            </span>
          </div>
          <Collapse open={s.fired}>
            <div className="sch-row">
              <span className="sch-row__chip">
                <Chip tone="run" dot>
                  <Roll>{s.run}</Roll>
                </Chip>
              </span>
              <span className="sch-row__main">
                <span className="sch-row__title">
                  <span className="sch-num">#58</span>
                  {TITLE}
                </span>
                <span className="sch-row__meta">
                  <span>stage</span>
                  <span>claude-opus-5-5</span>
                  <span>now</span>
                  {compact ? (
                    <span className="sch-row__link">
                      <IconRepeat />
                      schedule
                    </span>
                  ) : null}
                </span>
              </span>
              {compact ? null : (
                <span className="sch-row__order">
                  <Chip tone="plain">
                    <IconRepeat />
                    schedule
                  </Chip>
                </span>
              )}
            </div>
          </Collapse>
          <div className="sch-row sch-row--last">
            <span className="sch-row__chip">
              <Chip tone="run" dot>
                running
              </Chip>
            </span>
            <span className="sch-row__main">
              <span className="sch-row__title">
                <span className="sch-num">#57</span>
                <Bar w={compact ? 120 : 168} h={6} tone={3} />
              </span>
              <span className="sch-row__meta sch-row__meta--bars">
                <Bar w={40} h={5} tone={2} />
                <Bar w={96} h={5} tone={2} />
                <Bar w={28} h={5} tone={2} />
              </span>
            </span>
          </div>
        </section>

        <section className="sch-panel">
          <div className="sch-panel__head">
            <span className="sch-panel__title" data-tone="idle">
              <span className="sch-sq" />
              Scheduled · next 24h
            </span>
            <span className="sch-panel__meta">
              <Roll>{s.saved ? '2' : '1'}</Roll>
            </span>
          </div>
          <Collapse open={s.saved}>
            <div className="sch-row">
              <span className="sch-row__chip">
                <Chip tone="plain">
                  <IconRepeat />
                  repeat
                </Chip>
              </span>
              <span className="sch-row__main">
                <span className="sch-row__title">{TITLE}</span>
                <span className="sch-row__meta">
                  {compact ? null : <span>stage</span>}
                  <span>weekdays at 09:00</span>
                  <span>{ZONE}</span>
                  <span className="sch-next">
                    next <Roll>{next}</Roll>
                  </span>
                </span>
              </span>
              <span className="sch-row__actions">
                <Button size="small" className="sch-small">
                  Run now
                </Button>
              </span>
            </div>
          </Collapse>
          {/* Filler: it runs off the bottom once the spawned task opens, so
              nothing here is a word or an action */}
          <div className="sch-row sch-row--last">
            <span className="sch-row__chip">
              <Bar w={70} h={20} r={0} tone={2} />
            </span>
            <span className="sch-row__main">
              <span className="sch-row__title">
                <Bar w={compact ? 104 : 132} h={6} tone={3} />
              </span>
              <span className="sch-row__meta sch-row__meta--bars">
                <Bar w={40} h={5} tone={2} />
                <Bar w={110} h={5} tone={2} />
                <Bar w={72} h={5} tone={2} />
              </span>
            </span>
            <span className="sch-row__actions">
              <Bar w={58} h={24} r={0} tone={1} />
            </span>
          </div>
        </section>
      </div>
    </div>
  )
}

// The compact design is stage's phone layout on a phone, where the pointer is
// a finger. Compact switches on the container (below 600px); the landing
// column drops under that width with the viewport near 632px.
const NARROW = '(max-width: 631px)'
function subscribeNarrow(onChange: () => void) {
  const mq = window.matchMedia(NARROW)
  mq.addEventListener('change', onChange)
  return () => mq.removeEventListener('change', onChange)
}
function useNarrow(): boolean {
  return useSyncExternalStore(subscribeNarrow, () => window.matchMedia(NARROW).matches, () => false)
}

export function SchedulesDemo() {
  const theme = useSiteTheme()
  const narrow = useNarrow()
  return (
    <Stage<S>
      width={680}
      height={490}
      compact={{ below: 600, width: 358, height: 480 }}
      initial={initial}
      poster={poster}
      script={script}
      theme={theme}
      cursor={{ at: REST, style: narrow ? 'touch' : 'real' }}
      className="sch"
    >
      {(s, _rect, layout) =>
        layout === 'compact' ? (
          <Phone width={326} className="sch-phone" dark={theme === 'dark'} time={s.fired ? '9:00' : '8:59'}>
            <div className="sch-body">
              <Dashboard s={s} compact />
              <Drawer s={s} compact />
            </div>
          </Phone>
        ) : (
          <Window url="stage.local" className="sch-win" body={{ className: 'sch-body' }}>
            <Dashboard s={s} compact={false} />
            <Drawer s={s} compact={false} />
          </Window>
        )
      }
    </Stage>
  )
}
