import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router";
import { api } from "../api/client.ts";
import { useInvalidateWork, useTasks } from "../api/queries.ts";
import type { Task, TaskDetail, TaskState } from "../api/types.ts";
import { isFinal } from "../lib/tasks.ts";
import { IconChainLink } from "./icons.tsx";
import { taskStateLabel } from "./StateBadge.tsx";
import { Button, ErrorText, Muted, Panel, Select, cx, toneClass, type ChipTone } from "./ui.tsx";

/** Pipeline cell tint per step state; null = backlog (dashed, untinted). */
const STEP_TONE: Record<TaskState, ChipTone | null> = {
  backlog: null,
  launching: "run",
  running: "run",
  needs_input: "need",
  done: "done",
  settled: "ok",
  failed: "bad",
  interrupted: "bad",
  cancelled: "idle",
};

/** Tasks that can still gate or be gated (not settled/cancelled). */
const OPEN_STATES: TaskState[] = ["backlog", "launching", "running", "needs_input", "done", "failed", "interrupted"];

export const taskOption = (t: Task) => `#${t.id} ${t.title} · ${t.projectTitle} · ${taskStateLabel(t.state)}`;

/**
 * Link candidates for `self` (undefined while creating). `before`: tasks it can run after (open, no other
 * follower). `after`: backlog tasks with no predecessor that can run after it. The server rejects loops.
 */
export function useLinkCandidates(self: Task | undefined) {
  const open = useTasks({ state: OPEN_STATES });
  const all = open.data?.tasks ?? [];
  const before = all.filter((t) => t.id !== self?.id && (t.nextTaskId === null || t.nextTaskId === self?.id));
  const after = all.filter(
    (t) => t.id !== self?.id && t.state === "backlog" && (t.afterTaskId === null || t.afterTaskId === self?.id),
  );
  return { before, after, isPending: open.isPending };
}

/** A select of link candidates; "" = none. */
export function TaskPicker({
  id,
  value,
  onChange,
  options,
  none,
  disabled = false,
}: {
  id?: string;
  value: string;
  onChange: (value: string) => void;
  options: Task[];
  none: string;
  disabled?: boolean;
}) {
  return (
    <Select
      id={id}
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(e.target.value)}
      className="w-full min-w-0 truncate"
    >
      <option value="">{none}</option>
      {options.map((t) => (
        <option key={t.id} value={String(t.id)}>
          {taskOption(t)}
        </option>
      ))}
    </Select>
  );
}

/** Step cells joined by 1px lines; the viewed task gets an ink outline. Titles on laptop, numbers on phone. */
export function SequencePipeline({ steps, currentId }: { steps: Task[]; currentId: number }) {
  return (
    <ol aria-label="Run order" className="flex min-w-0 items-center overflow-x-auto p-0.5">
      {steps.map((s, i) => {
        const tone = STEP_TONE[s.state];
        const current = s.id === currentId;
        const label = `${i + 1}. ${s.title}: ${taskStateLabel(s.state)}${current ? " (this task)" : ""}`;
        return (
          <li key={s.id} className="flex min-w-0 items-center md:flex-1">
            {i === 0 ? null : <span aria-hidden className="h-px w-3 shrink-0 bg-line-strong md:w-4" />}
            <Link
              to={`/tasks/${s.id}`}
              title={label}
              aria-label={label}
              aria-current={current ? "page" : undefined}
              className={cx(
                "flex h-8 w-8 min-w-0 shrink-0 items-center justify-center gap-1.5 border md:w-auto md:flex-1 md:justify-start md:px-2",
                tone === null
                  ? "border-dashed border-line-strong text-muted hover:text-fg"
                  : cx(
                      toneClass(tone),
                      "border-[color-mix(in_srgb,var(--c)_55%,transparent)]",
                      current || tone === "bad" || tone === "need"
                        ? "bg-[color-mix(in_srgb,var(--c)_14%,transparent)] text-[var(--cf)]"
                        : "text-fg hover:bg-panel",
                    ),
                current && "outline outline-1 outline-offset-1 outline-fg",
              )}
            >
              {tone === null ? null : <span aria-hidden className="size-1.5 shrink-0 bg-[var(--c)] max-md:hidden" />}
              <span className="font-mono text-[11.5px] tabular">{i + 1}</span>
              <span className="hidden min-w-0 truncate text-[12.5px] font-medium md:inline">{s.title}</span>
            </Link>
          </li>
        );
      })}
    </ol>
  );
}

/** "step 2 of 3" when linked, else null. */
export function stepText(task: TaskDetail): string | null {
  if (task.sequence.length < 2) return null;
  const i = task.sequence.findIndex((t) => t.id === task.id);
  return `step ${i + 1} of ${task.sequence.length}`;
}

/** Run order panel on task detail: pipeline, predecessor/follower lines with unlink, pickers to link. */
export function SequencePanel({ task }: { task: TaskDetail }) {
  const invalidate = useInvalidateWork();
  const candidates = useLinkCandidates(task);
  const [before, setBefore] = useState("");
  const [after, setAfter] = useState("");
  const link = useMutation({
    mutationFn: ({ id, afterTaskId }: { id: number; afterTaskId: number | null }) => api.updateTask(id, { afterTaskId }),
    onSuccess: () => {
      setBefore("");
      setAfter("");
    },
    onSettled: invalidate,
  });
  const prev = task.sequence.find((t) => t.id === task.afterTaskId);
  const next = task.sequence.find((t) => t.id === task.nextTaskId);
  const canLinkBefore = task.state === "backlog" && task.afterTaskId === null;
  const canLinkAfter = !isFinal(task) && task.nextTaskId === null;
  const busy = link.isPending;

  return (
    <Panel
      title={
        <span className="inline-flex items-center gap-1.5">
          <IconChainLink />
          <span className="self-baseline">Run order</span>
        </span>
      }
      {...(stepText(task) === null ? {} : { meta: stepText(task) })}
    >
      <div className="flex flex-col gap-3">
        {task.sequence.length > 1 ? <SequencePipeline steps={task.sequence} currentId={task.id} /> : null}
        {prev === undefined && next === undefined && !canLinkBefore && !canLinkAfter ? (
          <Muted>Not linked. Settled and cancelled tasks can&apos;t be linked.</Muted>
        ) : null}
        {prev === undefined ? null : (
          <LinkLine
            label="Runs after"
            task={prev}
            hint={task.state === "backlog" ? "starts when it settles" : undefined}
            busy={busy}
            onUnlink={() => link.mutate({ id: task.id, afterTaskId: null })}
          />
        )}
        {next === undefined ? null : (
          <LinkLine
            label="Then runs"
            task={next}
            hint={next.state === "backlog" ? "starts when this settles" : undefined}
            busy={busy}
            onUnlink={() => link.mutate({ id: next.id, afterTaskId: null })}
          />
        )}
        {canLinkBefore ? (
          <PickRow
            label="Runs after"
            value={before}
            onChange={setBefore}
            options={candidates.before}
            none={candidates.before.length === 0 ? "No open tasks to wait on" : "Pick a task to wait on…"}
            busy={busy}
            onLink={() => link.mutate({ id: task.id, afterTaskId: Number(before) })}
          />
        ) : null}
        {canLinkAfter ? (
          <PickRow
            label="Then run"
            value={after}
            onChange={setAfter}
            options={candidates.after}
            none={candidates.after.length === 0 ? "No unlinked backlog tasks" : "Pick a backlog task to run next…"}
            busy={busy}
            onLink={() => link.mutate({ id: Number(after), afterTaskId: task.id })}
          />
        ) : null}
        <ErrorText error={link.error} />
      </div>
    </Panel>
  );
}

function LinkLine({
  label,
  task,
  hint,
  busy,
  onUnlink,
}: {
  label: string;
  task: Task;
  hint: string | undefined;
  busy: boolean;
  onUnlink: () => void;
}) {
  return (
    <div className="flex min-w-0 items-start gap-2">
      <div className="min-w-0 flex-1">
        <span className="label-caps">{label}</span>
        <Link to={`/tasks/${task.id}`} className="block truncate text-[13px] font-medium hover:underline" title={task.title}>
          <span className="font-mono text-[11.5px] text-muted">#{task.id}</span> {task.title}
        </Link>
        <span className="font-mono text-[11px] text-muted">
          {taskStateLabel(task.state)}
          {hint === undefined ? null : ` · ${hint}`}
        </span>
      </div>
      <Button small variant="ghost" disabled={busy} onClick={onUnlink}>
        Unlink
      </Button>
    </div>
  );
}

function PickRow({
  label,
  value,
  onChange,
  options,
  none,
  busy,
  onLink,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  options: Task[];
  none: string;
  busy: boolean;
  onLink: () => void;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <span className="label-caps">{label}</span>
      <div className="flex min-w-0 gap-2">
        <div className="min-w-0 flex-1">
          <TaskPicker value={value} onChange={onChange} options={options} none={none} disabled={options.length === 0} />
        </div>
        <Button disabled={busy || value === ""} onClick={onLink}>
          Link
        </Button>
      </div>
    </div>
  );
}
