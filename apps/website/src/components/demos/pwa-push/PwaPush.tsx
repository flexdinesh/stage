import { Bar, Button, Collapse, Expand, LockScreen, Notification, Phone, Roll, Stage, type Act, type Point } from '../../../vendor/stageshow'
import { StageMark } from '../../site/StageMark'
import { useSiteTheme } from '../shared/useSiteTheme'
import { IconBell, IconPlus, IconSearch, StageLogo } from './icons'
import './pwa-push.css'

// "Answer from your lock screen." A phone, pinned from the top, running off
// the bottom of the column. Three beats: a stage push lands on the lock
// screen (Needs input, the task and its project, as stage's notifier writes
// it); a tap launches the installed PWA out of the banner, landing on the
// task with its Needs input panel on top (2px yellow edge); an option and
// Send answer later, the request folds into "Answer sent to T3", the edge
// goes transparent and the task chip rolls to running. The loop resets the
// way a lock-screen story can: a swipe down from the top edge pulls the lock
// screen over the app, and the app is put back unseen behind it.
//
// Real strings: the push, the task title and meta, the two chips, the panel
// (header, question, options, footer) and the confirmation. The page below
// the panel is filler.

type S = {
  notif: boolean
  open: boolean
  pick: number
  busy: boolean
  sent: boolean
  running: boolean
  cover: boolean
}

const START: S = { notif: false, open: false, pick: -1, busy: false, sent: false, running: false, cover: false }
const POSTER: S = { ...START, notif: true, open: true, pick: 1, sent: true, running: true }

const REST = { wide: { x: 590, y: 440 }, compact: { x: 320, y: 410 } }

const TITLE = 'Needs input'
const BODY = 'Migrate auth tokens · api-gateway'

const OPTIONS = ['Revoke now', 'Grace period']

const mid = (r: { x: number; y: number; width: number; height: number } | null | undefined, fx: number, fy: number): Point | null =>
  r ? { x: r.x + r.width * fx, y: r.y + r.height * fy } : null

const script = async (a: Act<S>) => {
  await a.wait(700)
  a.mark('arrive')
  a.put({ notif: true })
  await a.wait(1700)
  a.mark('tap')
  await a.click({ at: 'notif', x: 0.72, y: 0.6 })
  await a.wait(90)
  a.put({ open: true })
  await a.wait(1600)
  a.mark('pick')
  await a.click({ at: 'opt-1', x: 0.8, y: 0.5 })
  a.put({ pick: 1 })
  await a.wait(800)
  a.mark('send')
  await a.click({ at: 'send', x: 0.82, y: 0.55 })
  a.put({ busy: true })
  await a.wait(520)
  a.mark('sent')
  a.put({ busy: false, sent: true })
  await a.wait(650)
  a.put({ running: true })
  await a.wait(2400)
  // Reset with a seen cause: a swipe down from the top edge brings the lock
  // screen down over the app
  a.mark('reset')
  const from = mid(a.rect('edge'), 0.5, 0.5)
  if (from) {
    await a.move(from, { duration: 260 })
    let pulled = false
    await a.drag({ x: from.x, y: from.y + 200 }, (_at, _from, t) => {
      if (!pulled && t > 0.08) {
        pulled = true
        a.put({ cover: true })
      }
    }, { duration: 380 })
  } else {
    a.put({ cover: true })
  }
  await a.wait(900)
  // Under the cover: put the app away and clear the push, then lift the
  // cover, unseen, off an identical lock screen
  await a.snap({ open: false, pick: -1, busy: false, sent: false, running: false, notif: false })
  await a.snap({ cover: false })
  void a.move(a.layout === 'compact' ? REST.compact : REST.wide, { duration: 300 })
  await a.wait(300)
}

function Push() {
  return <Notification icon={<StageLogo />} title={TITLE} body={BODY} />
}

function Radio({ on }: { on: boolean }) {
  return <span className="pp-radio" data-on={on || undefined} />
}

function App({ s }: { s: S }) {
  const waiting = !s.sent
  return (
    <div className="pp-app">
      <header className="pp-top">
        <span className="pp-mark">
          <StageMark needsInput={waiting} />
          stage
        </span>
        <span className="pp-top__end">
          <Button className="pp-btn pp-btn--icon" aria-label="Notifications">
            <IconBell />
          </Button>
          <Button className="pp-btn pp-btn--icon" aria-label="Search">
            <IconSearch />
          </Button>
          <Button className="pp-btn" variant="primary" icon={<IconPlus size={14} />}>
            Task
          </Button>
        </span>
      </header>

      <div className="pp-page">
        <div className="pp-head">
          <div className="pp-title">Migrate auth tokens</div>
          <div className="pp-meta">
            <b>//</b> api-gateway / task 42
          </div>
        </div>
        <div className="pp-chips">
          <span className="pp-chip pp-chip--dot" data-tone={s.running ? 'run' : 'need'}>
            <Roll>{s.running ? 'running' : 'needs input'}</Roll>
          </span>
          <span className="pp-chip">claude-opus-5-5</span>
        </div>

        <section className="pp-panel" data-waiting={waiting || undefined}>
          <div className="pp-panel__head">
            <span className="pp-label">
              <Roll>{waiting ? 'Needs input' : 'Answered'}</Roll>
            </span>
            <span className="pp-chip pp-chip--dot pp-fade" data-tone="need" data-off={!waiting || undefined}>
              1 question
            </span>
            <Button className="pp-btn pp-btn--small pp-fade pp-push-end" size="small" data-off={!waiting || undefined}>
              Open in T3
            </Button>
          </div>

          <Collapse open={s.sent}>
            <div className="pp-notice">
              <span className="pp-notice__mark" />
              <span className="pp-notice__text">
                <span className="pp-notice__title">Answer sent to T3</span>
                <span className="pp-notice__detail">Cutover: Grace period</span>
              </span>
            </div>
          </Collapse>

          <Collapse open={!s.sent}>
            <div className="pp-q">
              <div className="pp-q__legend">
                <span className="pp-chip pp-chip--plain">Cutover</span>
                <span className="pp-q__text">Revoke existing refresh tokens now?</span>
              </div>
              <ul className="pp-options">
                {OPTIONS.map((o, i) => (
                  <li key={o} className="pp-option" data-target={`opt-${i}`} data-on={s.pick === i || undefined}>
                    <Radio on={s.pick === i} />
                    <span className="pp-option__label">{o}</span>
                  </li>
                ))}
              </ul>
            </div>
            <div className="pp-foot">
              <span className="pp-foot__count">
                <Roll>{`${s.pick >= 0 ? 1 : 0}/1 answered`}</Roll>
              </span>
              <Button
                className="pp-btn pp-send"
                variant="primary"
                data-target="send"
                data-ready={s.pick >= 0 || undefined}
              >
                <span className="pp-send__label">
                  <span className="pp-busy" data-on={s.busy || undefined}>
                    <span className="pp-spin" />
                  </span>
                  Send answer
                </span>
              </Button>
            </div>
          </Collapse>
        </section>

        {/* The rest of task detail: filler under the panel */}
        <section className="pp-panel pp-panel--quiet">
          <div className="pp-panel__head">
            <Bar w={64} h={6} tone={3} />
            <Bar w={36} h={6} tone={2} style={{ marginLeft: 'auto' }} />
          </div>
          <div className="pp-filler">
            <Bar w="92%" h={7} tone={2} />
            <Bar w="84%" h={7} tone={2} />
            <Bar w="61%" h={7} tone={2} />
          </div>
        </section>
      </div>
    </div>
  )
}

export function PwaPush() {
  const theme = useSiteTheme()
  return (
    <Stage<S>
      width={680}
      height={480}
      compact={{ below: 600, width: 358, height: 440 }}
      initial={START}
      poster={POSTER}
      script={script}
      theme={theme}
      cursor={{ at: REST, style: 'touch' }}
      className="pp"
    >
      {(s, rect, layout) => (
        <Phone
          width={layout === 'compact' ? 326 : 380}
          frame="pro"
          dark={!s.open || s.cover || theme === 'dark'}
          wallpaper="var(--pp-wall)"
          className="pp-phone"
        >
          <LockScreen stack="top" controls={false} date="Sunday, November 15">
            <Notification
              icon={<StageLogo />}
              title={TITLE}
              body={BODY}
              className="pp-notif"
              data-target="notif"
              data-on={s.notif || undefined}
              data-gone={s.open || undefined}
            />
          </LockScreen>

          <div className="pp-launch" data-on={s.open || undefined}>
            <Expand open={s.open} from={rect('notif')} radius={23} face={<Push />} className="pp-expand">
              <App s={s} />
            </Expand>
          </div>

          <div className="pp-cover" data-on={s.cover || undefined} data-snap="">
            <LockScreen stack="top" controls={false} date="Sunday, November 15" />
          </div>

          {/* Where the reset swipe starts: the top edge, left of the island */}
          <span className="pp-edge" data-target="edge" />
        </Phone>
      )}
    </Stage>
  )
}
