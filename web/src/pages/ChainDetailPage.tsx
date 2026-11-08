import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { api, type ChainAction } from "../api/client.ts";
import { useChain, useInvalidateWork, useTasks } from "../api/queries.ts";
import type { Chain, Task } from "../api/types.ts";
import { ChainPipeline, sortedSteps, stepProgress } from "../components/ChainList.tsx";
import { IconChevronDown, IconPlus } from "../components/icons.tsx";
import { TaskFormModal } from "../components/TaskForm.tsx";
import { TaskRow } from "../components/TaskList.tsx";
import { Button, ErrorText, PageHeader, Panel, QueryState, Select, cx } from "../components/ui.tsx";
import { useIdParam } from "../lib/params.ts";
import { NotFoundPage } from "./NotFoundPage.tsx";

export function ChainDetailPage() {
  const id = useIdParam();
  return id === null ? <NotFoundPage /> : <ChainDetailView id={id} />;
}

function ChainDetailView({ id }: { id: number }) {
  const chain = useChain(id);
  return (
    <QueryState isPending={chain.isPending} error={chain.error}>
      {chain.data === undefined ? null : <ChainBody chain={chain.data} />}
    </QueryState>
  );
}

function swap(ids: number[], i: number, j: number): number[] {
  const next = [...ids];
  const a = next[i];
  const b = next[j];
  if (a === undefined || b === undefined) return ids;
  next[i] = b;
  next[j] = a;
  return next;
}

function ChainBody({ chain }: { chain: Chain }) {
  const invalidate = useInvalidateWork();
  const [creating, setCreating] = useState(false);
  const steps = sortedSteps(chain);
  const ids = steps.map((s) => s.id);

  const action = useMutation({
    mutationFn: (a: ChainAction) => api.chainAction(chain.id, a),
    onSettled: invalidate,
  });
  const setSteps = useMutation({
    mutationFn: (next: number[]) => api.updateChain(chain.id, { steps: next }),
    onSettled: invalidate,
  });
  const busy = setSteps.isPending;

  const controls: { a: ChainAction; label: string; show: boolean }[] = [
    { a: "start", label: "Start", show: chain.state === "idle" && steps.length > 0 },
    { a: "pause", label: "Pause", show: chain.state === "running" },
    { a: "resume", label: "Resume", show: chain.state === "paused" },
    { a: "skip", label: "Skip current step", show: chain.state !== "done" && chain.currentTaskId !== null },
  ];

  const stepButtons = (t: Task, i: number) => {
    const prev = steps[i - 1];
    const next = steps[i + 1];
    // Server rule: non-backlog steps keep their order and precede every backlog step, so only backlog steps swap.
    const canSwap = (other: Task | undefined) => other !== undefined && t.state === "backlog" && other.state === "backlog";
    return (
      <>
        <Button small variant="ghost" disabled={busy || !canSwap(prev)} onClick={() => setSteps.mutate(swap(ids, i, i - 1))} aria-label="Move up" title="Move up">
          <span className="inline-flex rotate-180">
            <IconChevronDown size={14} />
          </span>
        </Button>
        <Button small variant="ghost" disabled={busy || !canSwap(next)} onClick={() => setSteps.mutate(swap(ids, i, i + 1))} aria-label="Move down" title="Move down">
          <IconChevronDown size={14} />
        </Button>
        <Button
          small
          variant="ghost"
          disabled={busy || t.state !== "backlog"}
          title={t.state === "backlog" ? undefined : "Only backlog steps can be removed"}
          onClick={() => setSteps.mutate(ids.filter((x) => x !== t.id))}
        >
          Remove
        </Button>
      </>
    );
  };

  const shown = controls.filter((c) => c.show);
  const main = shown.find((c) => c.a !== "skip");

  return (
    <>
      <PageHeader
        title={chain.title}
        meta={`${chain.state} · ${stepProgress(chain)}`}
        actions={shown.map((c) => (
          <Button
            key={c.a}
            variant={c === main ? "primary" : "secondary"}
            busy={action.isPending && action.variables === c.a}
            disabled={action.isPending}
            onClick={() => action.mutate(c.a)}
          >
            {c.label}
          </Button>
        ))}
      />
      <div className="flex flex-col gap-4">
        <ChainPipeline chain={chain} size="lg" />
        <p className="text-[12.5px] text-muted">
          The next step starts when the previous step&apos;s thread is <strong className="font-semibold text-fg">settled in T3</strong>.
          A failed or interrupted step pauses the chain: restart that step or skip it, then resume.
        </p>
        <ErrorText error={action.error ?? setSteps.error} />
        <Panel
          flush
          title="Steps"
          meta={steps.length}
          actions={
            <Button small onClick={() => setCreating(true)}>
              <IconPlus size={14} /> New task into chain
            </Button>
          }
        >
          {steps.length === 0 ? (
            <p className="px-3 py-3 text-[12.5px] text-muted">No steps yet. Add a backlog task below or create a new one.</p>
          ) : (
            <ol>
              {steps.map((t, i) => {
                const current = t.id === chain.currentTaskId;
                return (
                  <li
                    key={t.id}
                    aria-current={current ? "step" : undefined}
                    className={cx("flex items-start border-b border-line last:border-b-0", current && "bg-panel")}
                  >
                    <span className="flex w-9 shrink-0 flex-col items-center pt-2 font-mono text-[12px] text-muted tabular">
                      <span className={cx(current && "font-bold text-fg")}>{i + 1}</span>
                      {current ? <span className="label-caps text-fg">now</span> : null}
                    </span>
                    <div className="min-w-0 flex-1">
                      <TaskRow task={t} extra={stepButtons(t, i)} allowBacklogStart={false} />
                    </div>
                  </li>
                );
              })}
            </ol>
          )}
          <AddExisting chainIds={ids} disabled={busy} onAdd={(taskId) => setSteps.mutate([...ids, taskId])} />
        </Panel>
      </div>
      {creating ? <TaskFormModal chainId={chain.id} onClose={() => setCreating(false)} /> : null}
    </>
  );
}

function AddExisting({ chainIds, disabled, onAdd }: { chainIds: number[]; disabled: boolean; onAdd: (id: number) => void }) {
  const backlog = useTasks({ state: ["backlog"] });
  const [selected, setSelected] = useState("");
  const eligible = (backlog.data?.tasks ?? []).filter((t) => t.chainId === null && !chainIds.includes(t.id));
  return (
    <div className="flex flex-wrap items-center gap-2 border-t border-line bg-panel px-3 py-2.5">
      <Select value={selected} onChange={(e) => setSelected(e.target.value)} className="min-w-60 flex-1" aria-label="Backlog task">
        <option value="">{eligible.length === 0 ? "No eligible backlog tasks" : "Add existing backlog task…"}</option>
        {eligible.map((t) => (
          <option key={t.id} value={String(t.id)}>
            {t.title} ({t.projectTitle})
          </option>
        ))}
      </Select>
      <Button
        disabled={disabled || selected === ""}
        onClick={() => {
          onAdd(Number(selected));
          setSelected("");
        }}
      >
        Add step
      </Button>
    </div>
  );
}
