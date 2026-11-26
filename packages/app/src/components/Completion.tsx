import { useMutation, useQuery } from "@tanstack/react-query";
import { lazy, Suspense, useId, useRef, useState } from "react";
import type { Acceptance, AcceptanceRequest, AcceptedEvidence, HarnessConnection, Task } from "../api/types.ts";
import { useScopeMachineId } from "../api/harnessScope.tsx";
import { useConnections, useHarnesses, useInvalidateWork, useSetAutoComplete } from "../api/queries.ts";
import { activeOn } from "../lib/harnesses.ts";
import { useApi } from "../platform/context.tsx";
import { provenanceText, stateMove } from "../lib/completion.ts";
import { dateTime, relativeTime } from "../lib/format.ts";
import { MAX_NOTE_BYTES, noteBytes, noteRequest } from "../lib/journal.ts";
import { isActive, statusChips, type StatusChip } from "../lib/tasks.ts";
import { IconArrowRight } from "./icons.tsx";
import { useLaunchGate } from "./LaunchGate.tsx";
import { statusChipView, taskStateLabel } from "./StateBadge.tsx";
import {
  Alert,
  Button,
  Checkbox,
  Chip,
  ChoiceRow,
  Drawer,
  EmptyState,
  ErrorText,
  Field,
  Hint,
  InlineError,
  Muted,
  Panel,
  QueryState,
  SubHeading,
  TextLink,
  Textarea,
  cx,
} from "./ui.tsx";

const ReplyMarkdown = lazy(() => import("./ReplyMarkdown.tsx"));

function Evidence({ evidence: e }: { evidence: AcceptedEvidence }) {
  const sessionId = e.session?.externalSessionId ?? e.externalSessionId;
  const earlier = e.runId === null || e.replyRunId === null || e.runId !== e.replyRunId;
  return <div className="flex min-w-0 flex-col gap-3">
    <p className="text-small">Execution at capture: {taskStateLabel(e.executionState)}.</p>
    <Hint>{e.observedAt === null ? "Execution observation unavailable." : `Cached execution observation: ${dateTime(e.observedAt)}.`} Later harness activity may have occurred.</Hint>
    {e.reply === null || e.reply.trim() === "" ? <Muted>Final reply missing. No reply was available in Stage.</Muted> : <>
      {earlier ? <Alert>Reply belongs to an earlier or unverified run. It is not a verified current-run result.</Alert> : null}
      <Suspense fallback={<p className="whitespace-pre-wrap break-words">{e.reply}</p>}><ReplyMarkdown text={e.reply} /></Suspense>
      {e.replyTruncated ? <Hint>Reply excerpt truncated at capture.</Hint> : null}
    </>}
    <div className="border-t border-line pt-3 text-small">
      <TextLink to={`/tasks/${e.taskId}?view=notes`} variant="rest">Task #{e.taskId} journal</TextLink>
      <Hint>{e.journalThroughId === 0 ? "No journal entries at capture." : `Journal context through entry #${e.journalThroughId}.`}</Hint>
      {e.treeId === null ? null : <TextLink to={`/trees/${e.treeId}?view=notes`} variant="rest">Tree #{e.treeId} journal</TextLink>}
      {e.branch === null ? null : <p className="font-mono text-meta break-all">Branch: {e.branch}</p>}
      {sessionId === null ? <Muted>No execution reference.</Muted> : <p className="font-mono text-meta break-all">Session: {sessionId}</p>}
    </div>
  </div>;
}

export function AcceptedResult({ acceptance, current, harness }: { acceptance: Acceptance; current: boolean; harness: Task["harness"] }) {
  const descriptor = useHarnesses().descriptor(harness);
  return <Panel title={current ? "Accepted result" : "Previous accepted result"} meta={`#${acceptance.id} · ${relativeTime(acceptance.acceptedAt)}`}>
    <div className="flex flex-col gap-3">
      <Muted>{provenanceText(acceptance.provenance, descriptor)}</Muted>
      {acceptance.note === "" ? null : <section><SubHeading title="Acceptance note" className="mb-1" /><p className="whitespace-pre-wrap break-words text-small">{acceptance.note}</p></section>}
      <Evidence evidence={acceptance.evidence} />
    </div>
  </Panel>;
}

export function CompletionButton({ task, review = false }: { task: Task; review?: boolean }) {
  const [open, setOpen] = useState(false);
  const completed = task.acceptanceId !== null;
  return <>
    <Button small={review} variant={completed || review || isActive(task) ? "secondary" : "primary"} disabled={!completed && isActive(task)} title={!completed && isActive(task) ? "Stop execution before completing this task." : undefined} onClick={() => setOpen(true)}>{completed ? "Change state" : review ? "Review" : "Complete task"}</Button>
    {open ? <CompletionDrawer task={task} reopen={completed} to={completed ? stateMove(task, new Set()).to : "completed"} onClose={() => setOpen(false)} /> : null}
  </>;
}

/** Confirm drawer for a state change: completing (evidence review and note) or leaving Completed ("Change state"). */
export function CompletionDrawer({ task, reopen, to, onClose }: { task: Task; reopen: boolean; to: StatusChip; onClose: () => void }) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const [note, setNote] = useState("");
  const pendingRequest = useRef<AcceptanceRequest | null>(null);
  const preview = useQuery({ queryKey: ["completion-preview", task.id], queryFn: () => api.completionPreview(task.id), staleTime: 0, refetchOnWindowFocus: false, refetchInterval: false });
  const change = useMutation({
    mutationFn: async () => {
      const p = preview.data;
      if (p === undefined) throw new Error("Load the evidence before confirming.");
      const old = pendingRequest.current;
      const body: AcceptanceRequest = old !== null && old.revision === p.revision && old.note === (reopen ? undefined : note) && old.evidenceVersion === (reopen ? undefined : p.evidenceVersion)
        ? old : { requestId: noteRequest(null, "acceptance", "decision").requestId, revision: p.revision, ...(reopen ? {} : { evidenceVersion: p.evidenceVersion, note }) };
      pendingRequest.current = body;
      return reopen ? api.reopenTask(task.id, body) : api.completeTask(task.id, body);
    },
    onSuccess: async () => { await invalidate(); onClose(); },
  });
  const tooLong = noteBytes(note) > MAX_NOTE_BYTES;
  const p = preview.data;
  return <Drawer title={reopen ? "Change state" : "Complete task"} onClose={onClose} footer={<>
    <Button variant="ghost" onClick={onClose} disabled={change.isPending}>Cancel</Button>
    <Button variant="primary" busy={change.isPending} disabled={p === undefined || tooLong || (!reopen && p.blockedReason !== "")} onClick={() => change.mutate()}>{reopen ? "Change state" : "Complete task"}</Button>
  </>}>
    <div className="flex flex-col gap-4">
      <p className="text-body font-medium break-words">#{task.id} {task.title}</p>
      <StateChange task={task} to={to} />
      <QueryState isPending={preview.isPending} error={preview.error} hasData={preview.data !== undefined}>
        {p === undefined ? null : <>
          {reopen ? <p className="text-small">Remove current acceptance. Previous results stay in the journal and accepted-result history. This does not start execution.</p> : <><Evidence evidence={p.evidence} /><Field label="Acceptance note (optional)">{(id) => <Textarea id={id} value={note} onChange={(event) => setNote(event.target.value)} rows={4} placeholder="Why you accept this work" />}</Field>{tooLong ? <InlineError>Keep the note under 16 KiB.</InlineError> : null}</>}
          {task.treeStatus !== null && task.treeStatus !== "open" ? <Alert>The tree stays closed. Reopen the tree separately to resume scheduling.</Alert> : null}
          <section className="border-t border-line pt-3"><SubHeading title="Dependent tasks" className="mb-1" />
            {p.dependents.length === 0 ? <EmptyState>No dependent tasks.</EmptyState> : <ul className="divide-y divide-line">{p.dependents.map((d) => <li key={d.taskId} className="py-2 text-small"><TextLink to={`/tasks/${d.taskId}`} variant="rest">#{d.taskId} {d.title}</TextLink><Muted>{d.claimed ? "Already claimed or run. Existing work and accepted inputs stay unchanged." : reopen ? "Blocked until this task is completed again." : d.startMode === "auto" ? "Starts automatically when every prerequisite is completed and the tree is open." : "Becomes ready when every prerequisite is completed; you start it."}</Muted></li>)}</ul>}
          </section>
          {!reopen && p.blockedReason !== "" ? <Alert>{p.blockedReason}</Alert> : null}
        </>}
      </QueryState>
      <ErrorText error={change.error} />
      <Button variant="secondary" busy={preview.isFetching} disabled={change.isPending} onClick={() => { change.reset(); void preview.refetch(); }}>Refresh evidence</Button>
    </div>
  </Drawer>;
}

/** "[to review] → [completed]": the state a confirm moves the task from and to. */
function StateChange({ task, to: toChip }: { task: Task; to: StatusChip }) {
  const blocked = useLaunchGate()(task) !== null;
  const from = statusChipView(statusChips(task, { blocked })[0] ?? task.state);
  const to = statusChipView(toChip);
  return (
    <p className="flex flex-wrap items-center gap-2">
      <Chip tone={from.tone} dot>{from.label}</Chip>
      <span className="text-muted"><IconArrowRight size={14} label="to" /></span>
      <Chip tone={to.tone} dot>{to.label}</Chip>
    </p>
  );
}

/**
 * Settings connection card toggle for the harness's auto-complete; saves at once. Labeled by the harness's declared
 * closure; hidden until the connection loads, and for a harness that declares no closure (nothing to infer from).
 */
export function AutoCompleteToggle({ harness, className }: { harness: HarnessConnection["harness"]; className?: string }) {
  const connections = useConnections();
  const closure = useHarnesses().descriptor(harness)?.closures[0];
  const save = useSetAutoComplete();
  const hintId = useId();
  const connection = activeOn(connections.data?.connections ?? [], harness, useScopeMachineId());
  if (connection === undefined || closure === undefined) return null;
  return (
    <div className={cx("flex min-w-0 flex-col gap-0.5", className)} aria-busy={save.isPending}>
      <ChoiceRow control={<Checkbox checked={connection.autoComplete} aria-describedby={hintId} onChange={(e) => save.mutate({ id: connection.id, on: e.target.checked })} />}>
        {closure.setting}
      </ChoiceRow>
      <Hint id={hintId}>Only successful runs. Change the task's state to undo it.</Hint>
      <ErrorText error={save.error} />
    </div>
  );
}
