import type { ReactNode } from "react";
import { Link } from "react-router";
import type { Chain, Task, TaskState } from "../api/types.ts";
import { relativeTime } from "../lib/format.ts";
import { ChainStateBadge, taskStateLabel } from "./StateBadge.tsx";
import { Panel, cx, toneClass, type ChipTone } from "./ui.tsx";

/** Pipeline cell tint per step state; null = backlog (dashed, untinted). */
const STEP_TONE: Record<TaskState, ChipTone | null> = {
  backlog: null,
  launching: "run",
  running: "run",
  needs_input: "need",
  completed: "done",
  settled: "ok",
  failed: "bad",
  interrupted: "bad",
  cancelled: "idle",
};

export const sortedSteps = (chain: Chain): Task[] =>
  chain.steps.toSorted((a, b) => (a.chainPosition ?? 0) - (b.chainPosition ?? 0));

/** `step x/y`: x = current step, or all when done, or passed count when idle/between steps. */
export function stepProgress(chain: Chain): string {
  const steps = sortedSteps(chain);
  const idx = steps.findIndex((s) => s.id === chain.currentTaskId);
  const passed = steps.filter((s) => s.state === "settled" || s.state === "cancelled").length;
  const x = idx >= 0 ? idx + 1 : chain.state === "done" ? steps.length : passed;
  return `step ${x}/${steps.length}`;
}

/** Step cells joined by 1px lines. `lg`: titles on laptop, numbers on phone. */
export function ChainPipeline({ chain, size = "sm" }: { chain: Chain; size?: "sm" | "lg" }) {
  const steps = sortedSteps(chain);
  if (steps.length === 0) return null;
  const lg = size === "lg";
  return (
    <ol aria-label="Step pipeline" className={cx("flex min-w-0 items-center", lg && "overflow-x-auto py-0.5")}>
      {steps.map((s, i) => {
        const tone = STEP_TONE[s.state];
        const current = s.id === chain.currentTaskId;
        const label = `${i + 1}. ${s.title}: ${taskStateLabel(s.state)}${current ? " (current)" : ""}`;
        return (
          <li key={s.id} className={cx("flex min-w-0 items-center", lg ? "md:flex-1" : "shrink-0")}>
            {i === 0 ? null : (
              <span
                aria-hidden
                className={cx("h-px shrink-0 bg-line-strong", lg ? "w-3 md:w-4" : "w-1.5")}
              />
            )}
            <span
              title={label}
              aria-label={label}
              className={cx(
                "flex min-w-0 items-center border",
                tone === null
                  ? "border-dashed border-line-strong text-muted"
                  : cx(
                      toneClass(tone),
                      "border-[color-mix(in_srgb,var(--c)_55%,transparent)]",
                      current || tone === "bad" || tone === "need"
                        ? "bg-[color-mix(in_srgb,var(--c)_14%,transparent)] text-[var(--cf)]"
                        : "text-fg",
                    ),
                current && "outline outline-1 outline-offset-1 outline-fg",
                lg ? "h-8 w-8 shrink-0 justify-center gap-1.5 md:w-auto md:flex-1 md:justify-start md:px-2" : "h-3.5 w-5",
              )}
            >
              {lg ? (
                <>
                  {tone === null ? null : <span aria-hidden className="size-1.5 shrink-0 bg-[var(--c)] max-md:hidden" />}
                  <span className="font-mono text-[11.5px] tabular">{i + 1}</span>
                  <span className="hidden min-w-0 truncate text-[12.5px] font-medium md:inline">{s.title}</span>
                </>
              ) : null}
            </span>
          </li>
        );
      })}
    </ol>
  );
}

export function ChainList({
  chains,
  title = "Chains",
  actions,
  emptyText = "No chains.",
}: {
  chains: Chain[];
  title?: string;
  actions?: ReactNode;
  emptyText?: string;
}) {
  return (
    <Panel flush title={title} meta={chains.length} actions={actions}>
      {chains.length === 0 ? (
        <p className="px-3 py-3 text-[12.5px] text-muted">{emptyText}</p>
      ) : (
        <ul>
          {chains.map((c) => {
            const current = c.steps.find((s) => s.id === c.currentTaskId);
            return (
              <li
                key={c.id}
                className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1.5 border-b border-line px-3 py-2 last:border-b-0 md:grid-cols-[118px_minmax(0,1fr)_auto_72px]"
              >
                <div className="col-start-2 row-start-1 justify-self-end md:col-start-1 md:justify-self-start">
                  <ChainStateBadge state={c.state} />
                </div>
                <div className="col-start-1 row-start-1 min-w-0 md:col-start-2">
                  <Link to={`/chains/${c.id}`} className="block truncate text-[13.5px] font-medium hover:underline">
                    {c.title}
                  </Link>
                  <div className="truncate font-mono text-[11.5px] text-muted">
                    {current === undefined ? null : <>current: {current.title} · </>}
                    updated {relativeTime(c.updatedAt)}
                  </div>
                </div>
                <div className="col-span-2 flex min-w-0 items-center gap-3 md:col-span-1 md:col-start-3 md:row-start-1">
                  <span className="font-mono text-[11.5px] text-muted tabular md:hidden">{stepProgress(c)}</span>
                  <ChainPipeline chain={c} />
                </div>
                <span className="hidden text-right font-mono text-[11.5px] text-muted tabular md:col-start-4 md:row-start-1 md:block">
                  {stepProgress(c)}
                </span>
              </li>
            );
          })}
        </ul>
      )}
    </Panel>
  );
}
