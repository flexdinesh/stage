import { Bar, Button, Expand, LockScreen, Notification, Phone, Roll, Stage, type Act, type Point } from '../../../vendor/stageshow'
import { StageMark } from '../../site/StageMark'
import { useSiteTheme } from '../shared/useSiteTheme'
import { IconBell, IconPlus, IconSearch, StageLogo } from './icons'
import './pwa-push.css'

// A push opens its Stage task, whose Open in T3 action continues in the harness.

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
          <div className="pp-panel__head"><span className="pp-label">{waiting ? 'Needs input' : 'Running'}</span></div>
          <div className="pp-q">{waiting ? 'Continue this conversation in T3.' : 'T3 resumed the task.'}</div>
          <div className="pp-foot"><Button className="pp-btn" variant="primary" data-target="send">Open in T3</Button></div>
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
