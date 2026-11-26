'use client'
import { Stage, Window, Bar, Roll, States, Collapse, Popover, Button, type Act } from '../../../vendor/stageshow'
import { useSiteTheme } from '../shared/useSiteTheme'
import { IconChainLink, IconChevronDown, IconCheck } from './icons'
import './run-order.css'

// Needs, told as stage does it: #13's Tree panel adds #12 as a need (picker,
// then Add). The rows pick up "needs #12" / "then #13" and the levels join.
// The user accepts #12 in Stage; explicitly automatic #13 launches, and its
// handoff (final reply, branch, session link) is the payoff.

type A = 'running' | 'done' | 'completed'
type B = 'backlog' | 'launching' | 'running'
type S = {
  open: boolean
  picked: boolean
  linked: boolean
  a: A
  b: B
  armed: boolean
  handoff: boolean
  hidden: boolean
}

const START: S = { open: false, picked: false, linked: false, a: 'running', b: 'backlog', armed: false, handoff: false, hidden: false }
const POSTER: S = { open: false, picked: true, linked: true, a: 'completed', b: 'running', armed: true, handoff: true, hidden: false }

type Tone = 'run' | 'done' | 'ok' | 'idle'
const TONE: Record<A | B, Tone> = { running: 'run', launching: 'run', done: 'done', completed: 'ok', backlog: 'idle' }

const script = async (a: Act<S>) => {
  await a.wait(900)
  a.mark('pick')
  await a.click({ at: 'picker', x: 0.9, y: 0.5 })
  a.put({ open: true })
  await a.wait(450)
  await a.move({ at: 'opt-12', x: 0.86, y: 0.5 })
  await a.wait(200)
  await a.click({ at: 'opt-12', x: 0.86, y: 0.5 })
  a.put({ picked: true })
  await a.wait(120)
  a.put({ open: false })
  await a.wait(350)
  await a.click({ at: 'link', x: 0.88, y: 0.82 })
  a.mark('link')
  a.put({ linked: true, armed: true })
  await a.wait(200)
  await a.move('rest-2')
  await a.wait(2300)
  // Execution finishes; Stage waits for the user to accept the result.
  a.mark('complete')
  a.put({ a: 'done' })
  await a.wait(1500)
  await a.click('complete')
  a.put({ a: 'completed' })
  await a.wait(1100)
  // #13 was waiting on exactly that: it launches with the handoff
  a.mark('start')
  a.put({ b: 'launching', handoff: true })
  await a.wait(1000)
  a.put({ b: 'running' })
  await a.wait(3200)
  // Reset behind a fade of the page body; the chrome holds
  a.mark('exit')
  a.put({ hidden: true })
  await a.wait(330)
  await a.snap({ ...START, hidden: true })
  await a.wait(120)
  a.put({ hidden: false })
  await a.move('rest')
  await a.wait(400)
}

function StateChip({ state }: { state: A | B }) {
  return (
    <span className="ro-chip ro-chip--dot" data-tone={TONE[state]}>
      <Roll variant="fade">{state}</Roll>
    </span>
  )
}

function SkeletonRow({ w, m }: { w: number; m: number }) {
  return (
    <div className="ro-row ro-row--filler">
      <span className="ro-row__state">
        <span className="ro-chip ro-chip--skel">
          <Bar w={46} h={5} tone={2} />
        </span>
      </span>
      <span className="ro-row__main">
        <span className="ro-row__title">
          <Bar w={22} h={5} tone={2} />
          <Bar w={w} h={6} tone={3} />
        </span>
        <span className="ro-row__meta">
          <Bar w={m} h={5} tone={2} />
        </span>
      </span>
    </div>
  )
}

export function Demo() {
  const theme = useSiteTheme()
  return (
    <Stage<S>
      width={680}
      height={480}
      compact={{ below: 600, width: 358, height: 461 }}
      initial={START}
      poster={POSTER}
      script={script}
      theme={theme}
      cursor={{ at: 'rest' }}
      className="ro"
    >
      {(s, rect, layout) => {
        const compact = layout === 'compact'
        const waiting = s.linked && s.b === 'backlog'
        const aTime = s.a === 'completed' ? 'completed just now' : '18m ago'
        const bTime = s.b === 'backlog' ? '2h ago' : 'just now'
        const aHint = s.b === 'backlog' ? `${s.a} · needs user completion` : s.a
        const row13 = rect('row-13')
        const satTop = row13.height > 0 ? row13.y + row13.height : undefined
        const page = (
              <div className="ro-page">
                <div className="ro-panel__head">
                  <span className="ro-label ro-label--ink">Tasks</span>
                  <span className="ro-panel__meta">8</span>
                </div>
                <div className="ro-rows" data-hidden={s.hidden || undefined}>
                <div className="ro-row">
                  <span className="ro-row__state">
                    <StateChip state={s.a} />
                  </span>
                  <span className="ro-row__main">
                    <span className="ro-row__title">
                      <span className="ro-num">#12</span>
                      <span className="ro-title">Retry failed webhooks</span>
                    </span>
                    <span className="ro-row__meta">
                      {compact ? null : <span>billing-api</span>}
                      {compact ? null : <span>claude-opus-5-5</span>}
                      <Roll variant="fade">{aTime}</Roll>
                      {compact ? <LinkHint label="then #13" show={s.linked} /> : null}
                    </span>
                  </span>
                  {s.a === 'done' ? <span className="ro-select" data-target="complete">Complete #12</span> : compact ? null : <LinkChip label="then #13" show={s.linked} />}
                </div>
                <div className="ro-row" data-target="row-13">
                  <span className="ro-row__state">
                    <StateChip state={s.b} />
                  </span>
                  <span className="ro-row__main">
                    <span className="ro-row__title">
                      <span className="ro-num">#13</span>
                      <span className="ro-title">Backfill the ledger</span>
                    </span>
                    <span className="ro-row__meta">
                      {compact ? null : <span>billing-api</span>}
                      {compact ? null : <span>gpt-6.1-sol</span>}
                      <Roll variant="fade">{bTime}</Roll>
                      {compact ? <LinkHint label="needs #12" show={waiting} /> : null}
                    </span>
                  </span>
                  {compact ? null : <LinkChip label="needs #12" show={waiting} />}
                </div>
                </div>
                <SkeletonRow w={150} m={170} />
                <SkeletonRow w={118} m={150} />
                <SkeletonRow w={164} m={140} />
                <SkeletonRow w={132} m={160} />
                <SkeletonRow w={146} m={128} />
                <SkeletonRow w={120} m={164} />
              </div>
        )
        return (
          <>
            {compact ? (
              <div className="ro-win ro-win--bare">{page}</div>
            ) : (
              <Window url="stage.local/projects/2" className="ro-win">
                {page}
              </Window>
            )}

            {/* #13's Tree panel, from its task detail page */}
            <section className="ro-sat" data-hidden={s.hidden || undefined} style={{ top: satTop }}>
              <div className="ro-panel__head">
                <IconChainLink />
                <span className="ro-label ro-label--ink">Tree</span>
                <span className="ro-panel__meta">
                  <Roll variant="fade">{s.linked ? '2 tasks · auto' : '#13'}</Roll>
                </span>
              </div>
              <div className="ro-sat__body">
                <Collapse open={!s.linked}>
                  <div className="ro-pick">
                    <span className="ro-label">Needs</span>
                    <div className="ro-pick__row">
                      <div className="ro-select" data-target="picker" data-open={s.open || undefined}>
                        <States
                          align="start"
                          value={s.picked ? 'picked' : 'empty'}
                          states={{
                            empty: <span className="ro-select__ph">Add a need…</span>,
                            picked: (
                              <span className="ro-select__val">
                                <span className="ro-num">#12</span> Retry failed webhooks
                              </span>
                            ),
                          }}
                        />
                        <IconChevronDown />
                      </div>
                      <Button size="large" className="ro-btn" data-target="link" data-off={!s.picked || undefined}>
                        Add
                      </Button>
                      <Popover show={s.open} className="ro-list">
                        <div className="ro-opt" data-target="opt-12">
                          <span className="ro-opt__title">
                            <span className="ro-num">#12</span> Retry failed webhooks
                          </span>
                          <span className="ro-opt__meta">billing-api · running</span>
                        </div>
                        <div className="ro-opt">
                          <span className="ro-opt__title">
                            <Bar w={20} h={5} tone={2} />
                            <Bar w={128} h={6} tone={3} />
                          </span>
                          <span className="ro-opt__meta">
                            <Bar w={104} h={5} tone={2} />
                          </span>
                        </div>
                        </Popover>
                    </div>
                  </div>
                </Collapse>

                <Collapse open={s.linked}>
                  <div className="ro-linked">
                    <ol className="ro-pipe" aria-label="Tree levels">
                      <li className="ro-step" data-tone={TONE[s.a]}>
                        <span className="ro-step__mark" />
                        <span className="ro-step__num">1</span>
                        {compact ? null : <span className="ro-step__title">Retry failed webhooks</span>}
                      </li>
                      <li className="ro-pipe__line" aria-hidden />
                      <li className="ro-step" data-current data-tone={s.b === 'backlog' ? 'none' : TONE[s.b]}>
                        <span className="ro-step__mark" />
                        <span className="ro-step__num">2</span>
                        {compact ? null : <span className="ro-step__title">Backfill the ledger</span>}
                      </li>
                    </ol>
                    <div className="ro-line">
                      <div className="ro-line__text">
                        <span className="ro-label">Needs</span>
                        <span className="ro-line__title">
                          <span className="ro-num">#12</span> Retry failed webhooks
                        </span>
                        <span className="ro-line__meta">
                          <Roll variant="fade">{aHint}</Roll>
                        </span>
                      </div>
                      <Button size="small" variant="ghost" className="ro-btn ro-btn--ghost">
                        Remove
                      </Button>
                    </div>
                  </div>
                </Collapse>

                <Collapse open={s.armed}>
                  {/* One slot: the checkbox row while #13 waits, the sent handoff once it launches */}
                  <div className="ro-after">
                    <States
                      align="start"
                      value={s.handoff ? 'record' : 'toggle'}
                      states={{
                        toggle: (
                          <div className="ro-toggle">
                            <span className="ro-toggle__row">
                              <span className="ro-check">
                                <IconCheck />
                              </span>
                              <span>Hand off context</span>
                            </span>
                            <span className="ro-hint">Appends #12&apos;s final reply, branch and session link at launch.</span>
                          </div>
                        ),
                        record: (
                          <div className="ro-handoff">
                            <div className="ro-handoff__head">
                              <span className="ro-label ro-label--ink">Handoff from #12</span>
                              <span className="ro-panel__meta">sent 14:02</span>
                              <Button size="small" className="ro-btn ro-btn--copy">
                                Copy handoff
                              </Button>
                            </div>
                            <p className="ro-handoff__reply">Retries back off from 2s to 64s, then dead-letter after 8 tries.</p>
                            <div className="ro-handoff__refs">
                              <span className="ro-chip">Session link</span>
                              <span className="ro-branch">
                                <span className="ro-branch__key">branch</span> feat/webhook-retries
                              </span>
                            </div>
                          </div>
                        ),
                      }}
                    />
                  </div>
                </Collapse>
              </div>
            </section>

            <span className="ro-rest" data-target="rest" />
            <span className="ro-rest ro-rest--2" data-target="rest-2" />
          </>
        )
      }}
    </Stage>
  )
}

/** Laptop needs chip: the row's own column, so titles never reflow. */
function LinkChip({ label, show }: { label: string; show: boolean }) {
  return (
    <span className="ro-row__link">
      <span className="ro-chip ro-chip--link" data-show={show || undefined}>
        <IconChainLink />
        <span>{label}</span>
      </span>
    </span>
  )
}

/** Phone rows print the chip in the meta line, icon and label in mono. */
function LinkHint({ label, show }: { label: string; show: boolean }) {
  return (
    <span className="ro-hintchip" data-show={show || undefined}>
      <IconChainLink />
      <span>{label}</span>
    </span>
  )
}
