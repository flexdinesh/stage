// Overview | Transcript switch for session and task detail (`?view=transcript`).
// The transcript itself (markdown renderer included) loads on first use.
import { Suspense, lazy } from "react";
import { useSearchParams } from "react-router";
import type { TaskState } from "../api/types.ts";
import { cx } from "./ui.tsx";

export type DetailView = "overview" | "transcript";

const LazyPanel = lazy(() => import("./Transcript.tsx").then((m) => ({ default: m.TranscriptPanel })));

/** Current view from the URL; switching pushes a history entry so Back returns. */
export function useDetailView(): [DetailView, (view: DetailView) => void] {
  const [params, setParams] = useSearchParams();
  const view: DetailView = params.get("view") === "transcript" ? "transcript" : "overview";
  const setView = (next: DetailView) => {
    const updated = new URLSearchParams(params);
    if (next === "transcript") updated.set("view", "transcript");
    else updated.delete("view");
    setParams(updated);
  };
  return [view, setView];
}

/** States in which T3 is still producing messages. */
export function isLiveState(state: TaskState): boolean {
  return state === "launching" || state === "running" || state === "needs_input";
}

const OPTIONS: readonly { value: DetailView; label: string }[] = [
  { value: "overview", label: "Overview" },
  { value: "transcript", label: "Transcript" },
];

/** Joined mono tabs (Segmented look); a run-tone square marks a live transcript. */
export function DetailViewTabs({ view, onChange, live }: { view: DetailView; onChange: (view: DetailView) => void; live: boolean }) {
  return (
    <div role="tablist" aria-label="Detail view" className="inline-flex max-w-full self-start border border-line-strong bg-surface">
      {OPTIONS.map((o) => {
        const selected = o.value === view;
        return (
          <button
            key={o.value}
            type="button"
            role="tab"
            aria-selected={selected}
            onClick={() => onChange(o.value)}
            className={cx(
              "inline-flex h-[38px] items-center gap-1.5 border-r border-line-strong px-3 font-mono text-detail whitespace-nowrap transition-colors duration-150 last:border-r-0 md:h-[30px] md:px-2.5",
              selected ? "bg-accent text-on-accent" : "text-muted hover:bg-panel hover:text-fg",
            )}
          >
            {o.label}
            {o.value === "transcript" && live ? <span aria-label="live" className="tone-run size-1.5 shrink-0 bg-[var(--c)]" /> : null}
          </button>
        );
      })}
    </div>
  );
}

function PanelSkeleton() {
  return (
    <div aria-busy="true" aria-label="Loading transcript" className="border border-line bg-surface">
      <div className="h-8 border-b border-line bg-panel" />
      <div className="flex flex-col gap-2 p-3">
        <div className="h-3 w-20 animate-pulse bg-panel" />
        <div className="h-3 w-full animate-pulse bg-panel" />
        <div className="h-3 w-2/3 animate-pulse bg-panel" />
      </div>
    </div>
  );
}

export function Transcript({ threadId, jumpOffset }: { threadId: string; jumpOffset: "session" | "task" }) {
  return (
    <Suspense fallback={<PanelSkeleton />}>
      <LazyPanel key={threadId} threadId={threadId} jumpOffset={jumpOffset} />
    </Suspense>
  );
}
