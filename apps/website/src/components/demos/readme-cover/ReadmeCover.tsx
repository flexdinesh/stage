'use client'
import { StageMark } from '../../site/StageMark'
import { Bar, Button, Collapse, Connector, Count, Roll, Stage, States, Stream, Window, type Act, type Rect } from '../../../vendor/stageshow'
import { IconCheck, IconPlus, IconSend } from './icons'
import './readme-cover.css'

// The README cover (README only, not on the landing): Claude in T3 Code and
// stage, talking through stage's MCP server. Beats: the ask is typed in T3's
// composer (its MCP chip says stage is connected) and sent; Claude answers
// and calls stage · create_task; a line labeled MCP runs from the call to
// stage, where the task lands at the top of the project's backlog. A beat
// later the cursor crosses to stage and presses the row's Start: the chip
// rolls to running, the button becomes Open in T3 Code, a line labeled start
// runs back, and the task's own session opens in T3 (a new thread in the
// sidebar, Claude already on it). The payoff holds; then T3 returns to the
// planning thread and the new row and thread fold away.
//
// Gold: overlap (stage's window in front of T3's right edge, both off the
// foot; the MCP and start labels sit on the seam between them) with ai-chat's bottom-anchored thread and Stream reply, backlog's row
// landing and Start. Recorded frame by frame by scripts/record-readme-cover.ts.
//
// Real strings: the ask, Claude's two lines, the tool call, the new row (id,
// title, model, chip, button), the new thread's title, origin and prompt,
// plus one neighbor row. The rest is placeholder.

const ASK = 'Create backlog task: add rate limit headers to /v1'
const REPLY = 'Adding it to the api-gateway backlog in stage.'
const TITLE = 'Add rate limit headers'
const PROMPT = 'Add rate limit headers to every /v1 response.'
const RUN = 'Reading middleware/limit.ts to reuse its token bucket.'
const REPLY_WORDS = REPLY.split(' ').length
const RUN_WORDS = RUN.split(' ').length

type S = {
  /** Composer text being typed */
  draft: string
  focus: boolean
  /** The ask has been sent */
  sent: boolean
  /** Reply words streamed */
  reply: number
  /** The tool call: 0 none, 1 running, 2 returned */
  call: 0 | 1 | 2
  /** The MCP line from the call to stage */
  mcp: boolean
  /** The task's row in stage */
  landed: boolean
  fresh: boolean
  started: boolean
  /** The start line from stage back to T3 */
  back: boolean
  /** T3 shows the task's own session */
  run: boolean
  /** Words of Claude's first line in that session */
  runReply: number
}

const initial: S = { draft: '', focus: false, sent: false, reply: 0, call: 0, mcp: false, landed: false, fresh: false, started: false, back: false, run: false, runReply: 0 }
const poster: S = { draft: '', focus: false, sent: true, reply: REPLY_WORDS, call: 2, mcp: false, landed: true, fresh: false, started: true, back: true, run: true, runReply: RUN_WORDS }

/** Rest: T3's thread, clear of what arrives there; PARK: T3's sidebar foot,
 * off the send button while Claude answers */
const REST = { x: 300, y: 296 }
const PARK = { x: 96, y: 392 }

async function script(a: Act<S>) {
  a.mark('start')
  await a.wait(450)
  await a.click({ at: 'composer', x: 0.72, y: 0.78 })
  a.put({ focus: true })
  await a.type('draft', ASK, { interval: 30 })
  await a.wait(260)
  await a.click({ at: 'send', x: 0.5, y: 0.5 })
  a.put({ sent: true, draft: '', focus: false })
  void a.move(PARK)
  await a.wait(420)
  for (let i = 1; i <= REPLY_WORDS; i++) {
    a.put({ reply: i })
    await a.wait(55)
  }
  await a.wait(240)
  a.mark('call')
  a.put({ call: 1 })
  await a.wait(620)
  a.put({ call: 2 })
  await a.wait(160)
  // The call reaches stage over MCP; the row lands as the line arrives
  a.mark('mcp')
  a.put({ mcp: true })
  await a.wait(720)
  a.put({ landed: true, fresh: true })
  await a.wait(1500)
  a.put({ mcp: false })
  await a.wait(350)
  // A beat later, in stage: Start
  a.mark('press')
  await a.move({ at: 'start', x: 0.5, y: 0.56 })
  await a.wait(140)
  await a.click()
  a.put({ started: true, fresh: false })
  await a.wait(320)
  a.mark('back')
  a.put({ back: true })
  await a.wait(780)
  a.put({ run: true })
  void a.move(REST)
  await a.wait(380)
  for (let i = 1; i <= RUN_WORDS; i++) {
    a.put({ runReply: i })
    await a.wait(60)
  }
  await a.wait(500)
  a.mark('poster')
  await a.wait(2400)
  // T3 goes back to the planning thread; the batch folds away. The planning
  // thread is behind the session view, so it starts over unseen first
  a.mark('exit')
  a.put({ back: false })
  // The line and its label fade before the unseen reset (a snap stops transitions)
  await a.wait(300)
  await a.snap({ sent: false, reply: 0, call: 0 })
  await a.wait(60)
  a.put({ run: false, landed: false, started: false })
  await a.wait(900)
  await a.snap({ ...initial })
  a.mark('loop')
  await a.wait(20)
}

/** Stage-space point on a rect's edge, `y` as a fraction of its height. */
function edge(r: Rect, side: 'left' | 'right', y = 0.5) {
  return { x: side === 'left' ? r.x : r.x + r.width, y: r.y + r.height * y }
}

/** Height of one stage row, shared with the CSS (.rc-row). */
const ROW = 56

export function ReadmeCover({ theme }: { theme: 'light' | 'dark' }) {
  return (
    <Stage<S> width={920} height={420} initial={initial} poster={poster} script={script} cursor={{ at: REST }} theme={theme} className="rc">
      {(s, rect) => {
        const call = rect('call')
        const rows = rect('rows')
        const runAsk = rect('run-ask')
        const rowAt = { x: rows.x, y: rows.y + ROW / 2 }
        return (
          <>
            <T3 s={s} />
            <StageApp s={s} />
            <Connector from={edge(call, 'right')} to={rowAt} route="curve" arrow show={s.mcp} label="MCP" className="rc-link" />
            <Connector from={rowAt} to={edge(runAsk, 'right')} route="curve" arrow show={s.back} label="start" className="rc-link" />
          </>
        )
      }}
    </Stage>
  )
}

/* ---- T3 Code, running Claude ---- */

function T3({ s }: { s: S }) {
  return (
    <Window
      className="rc-t3"
      sidebar={<Sidebar s={s} />}
      sidebarWidth={136}
      lead={
        <States
          value={s.run ? 'run' : 'plan'}
          align="start"
          states={{
            plan: <span className="rc-heading">Rate limits</span>,
            run: <span className="rc-heading">{TITLE}</span>,
          }}
        />
      }
      toolbar={<span className="rc-model">claude-opus-5-5</span>}
      body={{ className: 'rc-t3__body' }}
    >
      <div className="rc-view" data-on={!s.run || undefined}>
        <div className="rc-thread">
          <div className="rc-past">
            <div className="rc-ask">Where do we enforce rate limits?</div>
            <div className="rc-reply">In middleware/limit.ts, a token bucket per API key.</div>
          </div>
          <Collapse open={s.sent} reveal="drop" from="below">
            <div className="rc-ask">{ASK}</div>
          </Collapse>
          <Collapse open={s.reply > 0}>
            <Stream className="rc-reply" text={REPLY} count={s.reply} />
          </Collapse>
          <Collapse open={s.call > 0} reveal="drop" from="below">
            <Call state={s.call} live={s.mcp} />
          </Collapse>
        </div>
      </div>

      <div className="rc-view rc-view--run" data-on={s.run || undefined}>
        <div className="rc-origin">
          started from stage <b>#45</b>
        </div>
        <div className="rc-ask rc-ask--run" data-target="run-ask">
          {PROMPT}
        </div>
        <Collapse open={s.runReply > 0}>
          <Stream className="rc-reply" text={RUN} count={s.runReply} />
        </Collapse>
        <Collapse open={s.runReply >= RUN_WORDS}>
          <div className="rc-working">
            <span className="rc-spin" />
            <span>Working</span>
          </div>
        </Collapse>
      </div>

      <div className="rc-composer" data-focused={s.focus || undefined} data-target="composer" data-cursor="ibeam">
        <div className="rc-composer__text">
          {/* Mounted throughout: the placeholder fades on focus and back after send */}
          <span className="rc-placeholder" data-hide={s.draft !== '' || s.focus || undefined}>
            Ask Claude anything
          </span>
          {s.draft}
          {s.focus ? <span key={s.draft.length} className="rc-caret" /> : null}
        </div>
        <div className="rc-composer__foot">
          <span className="rc-mcp">
            <span className="rc-mcp__dot" />
            MCP · stage
          </span>
          <Button variant="primary" size="small" className="rc-btn rc-send" icon={<IconSend size={12} />} aria-label="Send" data-target="send" />
        </div>
      </div>
    </Window>
  )
}

function Sidebar({ s }: { s: S }) {
  return (
    <div className="rc-side">
      <div className="rc-side__brand">T3 Code</div>
      <div className="rc-side__label">api-gateway</div>
      <Collapse open={s.run}>
        <div className="rc-thread-row" data-on={s.run || undefined}>
          <span className="rc-spin" />
          <span className="rc-thread-row__name">{TITLE}</span>
        </div>
      </Collapse>
      <div className="rc-thread-row" data-on={!s.run || undefined}>
        <span className="rc-thread-row__name">Rate limits</span>
      </div>
      <div className="rc-thread-row">
        <span className="rc-thread-row__name">Webhook retries</span>
      </div>
      <div className="rc-thread-row">
        <span className="rc-thread-row__name">Session store</span>
      </div>
      {[78, 64].map((w, i) => (
        <div className="rc-thread-row" key={i}>
          <Bar w={w} h={6} tone={2} />
        </div>
      ))}
    </div>
  )
}

/** Claude's MCP call to stage, on two lines: server and tool, then what it made. */
function Call({ state, live }: { state: 0 | 1 | 2; live: boolean }) {
  return (
    <div className="rc-call" data-target="call" data-done={state === 2 || undefined} data-live={live || undefined}>
      <div className="rc-call__head">
        <span className="rc-call__mark">
          <States
            value={state === 2 ? 'done' : 'busy'}
            states={{
              busy: <span className="rc-spin" />,
              done: <IconCheck size={14} />,
            }}
          />
        </span>
        <span className="rc-call__fn">
          stage · <b>create_task</b>
        </span>
        <span className="rc-call__tag">MCP</span>
      </div>
      <div className="rc-call__body">
        <span className="rc-call__title">{TITLE}</span>
        <span className="rc-call__out">#45</span>
      </div>
    </div>
  )
}

/* ---- stage ---- */

function StageApp({ s }: { s: S }) {
  return (
    <Window url="stage.local/projects/3" className="rc-stage" body={{ className: 'rc-stage__body', 'data-crop': true }}>
      <div className="rc-top">
        <span className="rc-brand">
          <StageMark />
          <span className="rc-brand__name">stage</span>
          <span className="rc-brand__sep">/</span>
          <span className="rc-brand__project">api-gateway</span>
        </span>
        <Button variant="primary" size="small" className="rc-btn rc-new" icon={<IconPlus size={12} />}>
          New task
        </Button>
      </div>
      <section className="rc-panel">
        <div className="rc-panel__head">
          <span className="rc-panel__title">Tasks</span>
          <Count value={s.landed ? 6 : 5} className="rc-panel__count" />
        </div>
        <div className="rc-rows" data-target="rows">
          <Collapse open={s.landed} reveal="clip">
            <div className="rc-row" data-fresh={s.fresh || undefined}>
              <Chip started={s.started} />
              <div className="rc-row__main">
                <div className="rc-row__title">
                  <span className="rc-row__id">#45</span>
                  <span className="rc-row__name">{TITLE}</span>
                </div>
                <span className="rc-row__meta">claude-opus-5-5 · by agent</span>
              </div>
              <Button variant={s.started ? 'secondary' : 'primary'} size="small" className="rc-btn rc-start" data-target="start">
                {s.started ? 'Open in T3 Code' : 'Start'}
              </Button>
            </div>
          </Collapse>
          <Row id={44} title="Migrate session store to Redis" meta="claude-sonnet-5-5 · medium" />
          <Row id={41} title="Sign webhook payloads" meta="gpt-6.1-sol · high" />
          <Row id={38} title="Retry failed deliveries" meta="claude-sonnet-5-5 · low" />
          {[
            [36, 132, 92],
            [33, 108, 116],
          ].map(([id, title, meta]) => (
            <div className="rc-row" key={id}>
              <Chip started={false} />
              <div className="rc-row__main">
                <div className="rc-row__title">
                  <span className="rc-row__id">#{id}</span>
                  <Bar w={title} h={7} tone={2} />
                </div>
                <Bar w={meta} h={6} tone={1} className="rc-row__bar-meta" />
              </div>
              <span className="rc-row__slot" />
            </div>
          ))}
        </div>
      </section>
    </Window>
  )
}

/** A waiting backlog row, under the new one. */
function Row({ id, title, meta }: { id: number; title: string; meta: string }) {
  return (
    <div className="rc-row">
      <Chip started={false} />
      <div className="rc-row__main">
        <div className="rc-row__title">
          <span className="rc-row__id">#{id}</span>
          <span className="rc-row__name">{title}</span>
        </div>
        <span className="rc-row__meta">{meta}</span>
      </div>
      <Button variant="primary" size="small" className="rc-btn">
        Start
      </Button>
    </div>
  )
}

function Chip({ started }: { started: boolean }) {
  return (
    <span className="rc-chip" data-tone={started ? 'run' : 'idle'}>
      <Roll>{started ? 'running' : 'backlog'}</Roll>
    </span>
  )
}
