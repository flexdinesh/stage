import { useState } from "react";
import type { ThreadRun } from "../api/types.ts";
import { dateTime, relativeTime } from "../lib/format.ts";
import { RUNS_COLLAPSED, runModel, runQueued, runRan } from "../lib/pulls.ts";
import { ThreadStatusBadge } from "./StateBadge.tsx";
import { Button, Panel } from "./ui.tsx";

/**
 * A thread's runs (each follow-up is a new run), newest first: the five newest, then every stored run (at most 50)
 * behind Show all. `runCount` is T3's total, which can exceed what is stored.
 */
export function RunsPanel({ runs, runCount }: { runs: ThreadRun[]; runCount: number }) {
  const [all, setAll] = useState(false);
  const total = Math.max(runCount, runs.length);
  const shown = all ? runs : runs.slice(0, RUNS_COLLAPSED);
  const expandable = runs.length > RUNS_COLLAPSED;
  return (
    <Panel
      title="Runs"
      meta={<span className="tabular">{total}</span>}
      actions={
        expandable ? (
          <Button small variant="ghost" aria-expanded={all} onClick={() => setAll((v) => !v)}>
            {all ? "Show fewer" : `Show all ${runs.length}`}
          </Button>
        ) : undefined
      }
      flush
    >
      {runs.length === 0 ? (
        <p className="px-3 py-2.5 text-small text-muted">No runs yet.</p>
      ) : (
        <>
          <ul>
            {shown.map((r) => (
              <li key={r.runId} className="border-b border-line last:border-b-0">
                <RunRow run={r} />
              </li>
            ))}
          </ul>
          {(all || !expandable) && total > runs.length ? (
            <p className="border-t border-line px-3 py-2 text-small text-muted">
              {total - runs.length} older run{total - runs.length === 1 ? "" : "s"} only in T3.
            </p>
          ) : null}
        </>
      )}
    </Panel>
  );
}

function RunRow({ run }: { run: ThreadRun }) {
  const model = runModel(run);
  const ran = runRan(run);
  const queued = runQueued(run);
  const startedAt = run.startedAt ?? run.requestedAt;
  return (
    <div className="grid grid-cols-[36px_auto_minmax(0,1fr)] items-center gap-x-3 gap-y-1 px-3 py-2 font-mono text-meta text-muted md:grid-cols-[36px_118px_minmax(0,1fr)_76px_84px_92px] md:gap-x-3.5">
      <span title={run.runId} className="order-1 text-fg tabular">
        #{run.ordinal}
      </span>
      <span className="order-2">
        <ThreadStatusBadge status={run.status} settled={false} />
      </span>
      <time
        dateTime={startedAt}
        title={`${run.startedAt === null ? "Requested" : "Started"} ${dateTime(startedAt)}`}
        className="order-3 justify-self-end tabular md:order-4 md:justify-self-start"
      >
        {run.startedAt === null ? "queued " : ""}
        {relativeTime(startedAt)}
      </time>
      <div className="order-4 col-span-2 col-start-2 flex min-w-0 flex-wrap gap-x-2.5 md:contents">
        <span title={model} className="min-w-0 truncate md:order-3">
          {model}
        </span>
        <span
          title={run.startedAt === null ? undefined : `Ran ${dateTime(run.startedAt)} to ${run.completedAt === null ? "now" : dateTime(run.completedAt)}`}
          className="tabular md:order-5"
        >
          {ran === null ? "" : `ran ${ran}`}
        </span>
        <span title={queued === null ? undefined : `Requested ${dateTime(run.requestedAt)}`} className="tabular md:order-6">
          {queued === null ? "" : `queued ${queued}`}
        </span>
      </div>
    </div>
  );
}
