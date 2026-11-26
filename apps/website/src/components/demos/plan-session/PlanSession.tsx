'use client'
import { Bar, Button, Collapse, Count, Roll, Stage, States, Stream, Window, type Act } from '../../../vendor/stageshow'
import { useSiteTheme } from '../shared/useSiteTheme'
import { IconCheck } from './icons'
import './plan-session.css'

// A planning session fans a job out across models. Beats: the agent says how
// it will split the ask; four create_task calls stream in, each landing as a
// backlog row in stage's task list; create_tree 41 43:41 42:43 makes
// the first three a tree where each needs the one before (the header rolls
// Backlog to the tree's title, the rows slide into level order, the step
// cells join); Start ready is pressed and the root goes running. The task list holds, then leaves around the ask.
//
// Gold: overlap (a smaller card in front of a window, straddling its edge)
// with ai-chat's bottom-anchored thread and Stream reply. The session window
// is whole (no sidebar); the panel straddles its right edge and stops on the
// stage's right gutter.
//
// Real strings: the thread title, the ask, the reply, the five tool calls
// (task titles) and the panel rows that mirror them (titles and models). The
// earlier turns are placeholder.

type Task = { id: number; title: string; model: string }

const TASKS: Task[] = [
  { id: 41, title: 'Add sessions table', model: 'claude-opus-5-5 · high' },
  { id: 42, title: 'Swap auth guard', model: 'gpt-6.1-sol' },
  { id: 43, title: 'Backfill sessions', model: 'claude-sonnet-5-5' },
  { id: 44, title: 'Update auth docs', model: 'claude-haiku-5-5' },
]
/** Level order after create_tree 41 43:41 42:43: indexes into TASKS. */
const CHAIN = [0, 2, 1]
const CALLS = TASKS.length + 1

const ASK = 'Plan the move from JWT to server sessions and queue it in stage.'
const REPLY = 'Four pieces: three run in order, the docs can go on their own. Queuing them in stage.'
const WORDS = REPLY.split(' ').length

type S = {
  /** Reply words streamed */
  reply: number
  /** Tool calls shown */
  calls: number
  /** Tool calls finished */
  done: number
  ordered: boolean
  linked: boolean
  started: boolean
  /** The task list leaving at the loop's end */
  out: boolean
}

const initial: S = { reply: 0, calls: 0, done: 0, ordered: false, linked: false, started: false, out: false }
const poster: S = { reply: WORDS, calls: CALLS, done: CALLS, ordered: true, linked: true, started: true, out: false }

/** Earlier turns as placeholder: they fill the window at the start and go up
 * out of it as the calls arrive. [ask width, reply line widths] */
const PAST: [number, string, string][] = [
  [150, '74%', '50%'],
  [210, '86%', '62%'],
  [170, '80%', '44%'],
]

/** Compact's placeholder rows before the tasks land: [title, model] bar widths */
const GHOSTS: [number, number][] = [
  [128, 96],
  [104, 72],
  [120, 108],
  [112, 88],
]

const REST = { wide: { x: 604, y: 46 }, compact: { x: 322, y: 36 } }

async function script(a: Act<S>) {
  await a.wait(300)
  a.mark('reply')
  // Compact drops the reply line: the calls follow the ask at once
  if (a.layout === 'compact') a.put({ reply: WORDS })
  else
    for (let i = 1; i <= WORDS; i++) {
      a.put({ reply: i })
      await a.wait(42)
    }
  await a.wait(a.layout === 'compact' ? 0 : 400)
  for (let i = 0; i < TASKS.length; i++) {
    a.mark(`call-${i}`)
    a.put({ calls: i + 1 })
    await a.wait(560)
    a.put({ done: i + 1 })
    await a.wait(420)
  }
  a.mark('order')
  a.put({ calls: CALLS })
  await a.wait(620)
  a.put({ done: CALLS })
  await a.wait(160)
  a.put({ ordered: true })
  await a.wait(620)
  a.put({ linked: true })
  await a.wait(500)
  a.mark('start')
  await a.move({ at: 'start', x: 0.78, y: 0.66 })
  await a.wait(160)
  await a.click()
  a.put({ started: true })
  await a.wait(300)
  await a.move(REST)
  a.mark('payoff')
  await a.wait(2400)
  // The task list leaves around the ask: panel first, then the calls, then the reply
  a.mark('exit')
  // Compact keeps its panel: it eases back to its opening slots instead
  a.put(a.layout === 'compact' ? { out: true, ordered: false, linked: false, started: false } : { out: true })
  await a.wait(90)
  a.put({ calls: 0 })
  await a.wait(90)
  a.put({ reply: 0 })
  await a.wait(480)
  await a.snap({ ...initial })
}

/** Panel geometry: row and section-header heights, shared with the CSS. */
const SH = 41
const RH = 52

function rowY(i: number, ordered: boolean): number {
  if (!ordered) return SH + i * RH
  const step = CHAIN.indexOf(i)
  return step >= 0 ? SH + step * RH : 2 * SH + CHAIN.length * RH
}

export function PlanSession() {
  const theme = useSiteTheme()
  return (
    <Stage
      width={680}
      height={480}
      compact={{ below: 600, width: 358, height: 600 }}
      initial={initial}
      poster={poster}
      script={script}
      cursor={{ at: REST }}
      theme={theme}
      className="ps"
    >
      {(s, _rect, layout) => {
        const compact = layout === 'compact'
        const landed = Math.min(s.done, TASKS.length)
        // Compact: the panel holds its slot from the first frame (placeholder
        // rows the real ones replace in place), so the ground under the
        // window is never empty; the rows leave at the loop's end, not it
        const panel = compact || (s.done > 0 && !s.out)
        const shown = compact && s.out ? 0 : landed
        const bodyH = s.ordered ? 2 * SH + TASKS.length * RH : SH + (compact ? TASKS.length : Math.max(landed, 1)) * RH
        return (
          <>
            <Window className="ps-win" title="Migrate auth to sessions" toolbar={<span className="ps-tool">api-gateway</span>}>
              <div className="ps-body">
                <div className="ps-thread">
                  {PAST.map(([q, a1, a2], i) => (
                    <div className="ps-past" key={i}>
                      <Bar w={compact ? q - 40 : q} h={30} tone={1} className="ps-past-q" />
                      <Bar w={a1} h={7} tone={2} />
                      <Bar w={a2} h={7} tone={2} />
                    </div>
                  ))}
                  <div className="ps-ask">{ASK}</div>
                  {compact ? null : (
                    <Collapse open={s.reply > 0}>
                      <Stream className="ps-reply" text={REPLY} count={s.reply} />
                    </Collapse>
                  )}
                  <Collapse open={s.calls > 0} reveal="drop" from="below">
                    <div className="ps-calls">
                      {TASKS.map((t, i) => (
                        <Collapse key={t.id} open={s.calls > i} className="ps-call-wrap">
                          <Call done={s.done > i} fn="create_task" title={t.title} out={`#${t.id}`} />
                        </Collapse>
                      ))}
                      <Collapse open={s.calls > TASKS.length} className="ps-call-wrap">
                        <Call done={s.done > TASKS.length} fn="create_tree" title="41 43:41 42:43" mono out="Auth" />
                      </Collapse>
                    </div>
                  </Collapse>
                </div>
                <div className="ps-composer">
                  <Bar w={132} h={7} tone={2} />
                </div>
              </div>
            </Window>

            <section className="ps-panel" data-show={panel || undefined}>
              <div className="ps-panel__head">
                <span className="ps-label ps-label--ink">Tasks</span>
                <span className="ps-meta">
                  <Count value={shown} />
                </span>
              </div>
              <div className="ps-panel__body" style={{ height: bodyH }}>
                <div className="ps-sec" style={{ top: 0 }}>
                  {/* The backlog header becomes the tree's header: the two crossfade in place */}
                  <States
                    value={s.ordered ? 'chain' : 'loose'}
                    align="start"
                    states={{
                      loose: (
                        <>
                          <span className="ps-label">Backlog</span>
                          <span className="ps-meta">
                            <Count value={shown} />
                          </span>
                        </>
                      ),
                      chain: (
                        <>
                          <span className="ps-label">Auth</span>
                          <span className="ps-meta">3 tasks</span>
                        </>
                      ),
                    }}
                  />
                  <Button
                    size="small"
                    className="ps-start"
                    data-target="start"
                    data-show={(s.ordered && !s.started) || undefined}
                  >
                    Start ready
                  </Button>
                </div>
                <div className="ps-sec ps-sec--late" style={{ top: SH + CHAIN.length * RH }} data-show={s.ordered || undefined}>
                  <span className="ps-label">Backlog</span>
                  <span className="ps-meta">1</span>
                </div>
                {compact
                  ? GHOSTS.map(([title, model], i) => (
                      <div key={i} className="ps-ghost" data-show={shown <= i || undefined} data-sep={i < TASKS.length - 1 || undefined} style={{ top: SH + i * RH }}>
                        <span className="ps-lead">
                          <span className="ps-check" />
                        </span>
                        <Bar w={title} h={7} tone={2} className="ps-ghost__title" />
                        <Bar w={model} h={6} tone={1} className="ps-ghost__model" />
                      </div>
                    ))
                  : null}
                {TASKS.map((t, i) => {
                  const step = CHAIN.indexOf(i)
                  const chained = s.ordered && step >= 0
                  const running = s.started && step === 0
                  return (
                    <div
                      key={t.id}
                      className="ps-row"
                      data-show={shown > i || undefined}
                      data-sep={(s.ordered ? step >= 0 : i < (compact ? TASKS.length : landed) - 1) || undefined}
                      style={{ translate: `0 ${rowY(i, s.ordered)}px`, zIndex: i === 2 ? 2 : 1 }}
                    >
                      <span className="ps-lead">
                        <States
                          value={chained ? 'step' : 'box'}
                          states={{
                            box: <span className="ps-check" />,
                            step: (
                              <span className="ps-step" data-solid={running || undefined}>
                                {step + 1}
                              </span>
                            ),
                          }}
                        />
                      </span>
                      <span className="ps-row__line">
                        <span className="ps-id">#{t.id}</span>
                        <span className="ps-title">{t.title}</span>
                        <span className="ps-chip" data-tone={running ? 'run' : 'idle'}>
                          <Roll>{running ? 'running' : 'backlog'}</Roll>
                        </span>
                      </span>
                      <span className="ps-model">{t.model}</span>
                    </div>
                  )
                })}
                {/* The tree's levels: 1px connectors joining the step cells */}
                {CHAIN.slice(1).map((_, k) => (
                  <span
                    key={k}
                    className="ps-link"
                    data-on={(s.ordered && s.linked) || undefined}
                    style={{ top: SH + k * RH + 23, height: RH - 16, transitionDelay: `${k * 120}ms` }}
                  />
                ))}
              </div>
            </section>
          </>
        )
      }}
    </Stage>
  )
}

/** One MCP call on one line: the tool, its subject, then what it returned. */
function Call({ done, fn, title, out, mono = false }: { done: boolean; fn: string; title: string; out: string; mono?: boolean }) {
  return (
    <div className="ps-call" data-done={done || undefined}>
      <span className="ps-call__mark">
        <States
          value={done ? 'done' : 'busy'}
          states={{
            busy: <span className="ps-spin" />,
            done: <IconCheck size={14} />,
          }}
        />
      </span>
      <span className="ps-call__fn">{fn}</span>
      <span className={mono ? 'ps-call__args' : 'ps-call__title'}>{title}</span>
      <span className="ps-call__out">{out}</span>
    </div>
  )
}
