import { useMutation, useQuery } from "@tanstack/react-query";
import { lazy, Suspense, useRef, useState } from "react";
import { Link } from "react-router";
import type { Acceptance, AcceptanceRequest, AcceptedEvidence, Task } from "../api/types.ts";
import { useInvalidateWork } from "../api/queries.ts";
import { useApi } from "../platform/context.tsx";
import { dateTime, relativeTime } from "../lib/format.ts";
import { MAX_NOTE_BYTES, noteBytes, noteRequest } from "../lib/journal.ts";
import { isActive } from "../lib/tasks.ts";
import { taskStateLabel } from "./StateBadge.tsx";
import { Alert, Button, Drawer, ErrorText, Field, Panel, QueryState, Textarea } from "./ui.tsx";

const ReplyMarkdown = lazy(() => import("./ReplyMarkdown.tsx"));

function Evidence({ evidence: e }: { evidence: AcceptedEvidence }) {
  const earlier = e.runId === null || e.replyRunId === null || e.runId !== e.replyRunId;
  return <div className="flex min-w-0 flex-col gap-3">
    <p className="text-small">Execution at capture: {taskStateLabel(e.executionState)}.</p>
    <p className="font-mono text-meta text-muted">{e.observedAt === null ? "Execution observation unavailable." : `Cached execution observation: ${dateTime(e.observedAt)}.`} May predate the latest T3 activity.</p>
    {e.reply === null || e.reply.trim() === "" ? <p className="text-small text-muted">Final reply missing. No reply was available in Stage.</p> : <>
      {earlier ? <Alert>Reply belongs to an earlier or unverified run. It is not a verified current-run result.</Alert> : null}
      <Suspense fallback={<p className="whitespace-pre-wrap break-words">{e.reply}</p>}><ReplyMarkdown text={e.reply} /></Suspense>
      {e.replyTruncated ? <p className="font-mono text-meta text-muted">Reply excerpt truncated at capture.</p> : null}
    </>}
    <div className="border-t border-line pt-3 text-small">
      <Link to={`/tasks/${e.taskId}?view=notes`} className="underline">Task #{e.taskId} journal</Link>
      <p className="font-mono text-meta text-muted">{e.journalThroughId === 0 ? "No journal entries at capture." : `Journal context through entry #${e.journalThroughId}.`}</p>
      {e.treeId === null ? null : <Link to={`/trees/${e.treeId}?view=notes`} className="underline">Tree #{e.treeId} journal</Link>}
      {e.branch === null ? null : <p className="font-mono text-meta break-all">Branch: {e.branch}</p>}
      {e.threadId === null ? <p className="text-muted">No execution reference.</p> : <p className="font-mono text-meta break-all">T3: {e.threadId}</p>}
      {e.artifacts.length === 0 ? <p className="text-muted">No artifact links captured.</p> : <ul className="mt-2 space-y-1">{e.artifacts.map((link) => <li key={link.url}><a href={link.url} target="_blank" rel="noopener noreferrer" className="break-all underline">{link.repository || link.url}{link.number > 0 ? ` #${link.number}` : ""}</a></li>)}</ul>}
    </div>
  </div>;
}

export function AcceptedResult({ acceptance, current }: { acceptance: Acceptance; current: boolean }) {
  return <Panel title={current ? "Accepted result" : "Previous accepted result"} meta={`#${acceptance.id} · ${relativeTime(acceptance.acceptedAt)}`}>
    <div className="flex flex-col gap-3">
      <p className="text-small text-muted">{acceptance.provenance === "historical_settlement" ? "Migrated from historical T3 settlement. Evidence reflects the local cache at migration." : "Completed by you in Stage. This result stays unchanged when execution continues."}</p>
      {acceptance.note === "" ? null : <section><h3 className="label-caps mb-2">Acceptance note</h3><p className="whitespace-pre-wrap break-words text-small">{acceptance.note}</p></section>}
      <Evidence evidence={acceptance.evidence} />
    </div>
  </Panel>;
}

export function CompletionButton({ task }: { task: Task }) {
  const [open, setOpen] = useState(false);
  const completed = task.acceptanceId !== null;
  return <>
    <Button variant={completed || isActive(task) ? "secondary" : "primary"} disabled={!completed && isActive(task)} title={!completed && isActive(task) ? "Stop execution before completing this task." : undefined} onClick={() => setOpen(true)}>{completed ? "Reopen task" : "Complete task"}</Button>
    {open ? <CompletionDrawer task={task} reopen={completed} onClose={() => setOpen(false)} /> : null}
  </>;
}

function CompletionDrawer({ task, reopen, onClose }: { task: Task; reopen: boolean; onClose: () => void }) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const [note, setNote] = useState("");
  const pendingRequest = useRef<AcceptanceRequest | null>(null);
  const preview = useQuery({ queryKey: ["completion-preview", task.id], queryFn: () => api.completionPreview(task.id), staleTime: 0, refetchOnWindowFocus: false });
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
  return <Drawer title={reopen ? "Reopen task" : "Complete task"} onClose={onClose} footer={<>
    <Button variant="ghost" onClick={onClose} disabled={change.isPending}>Cancel</Button>
    <Button variant="primary" busy={change.isPending} disabled={p === undefined || tooLong || (!reopen && p.blockedReason !== "")} onClick={() => change.mutate()}>{reopen ? "Reopen task" : "Complete task"}</Button>
  </>}>
    <div className="flex flex-col gap-4">
      <p className="text-body font-medium break-words">#{task.id} {task.title}</p>
      <QueryState isPending={preview.isPending} error={preview.error}>
        {p === undefined ? null : <>
          {reopen ? <p className="text-small">Remove current acceptance. Previous results stay in the journal and accepted-result history. This does not start execution.</p> : <><Evidence evidence={p.evidence} /><Field label="Acceptance note (optional)">{(id) => <Textarea id={id} value={note} onChange={(event) => setNote(event.target.value)} rows={4} placeholder="Why you accept this work" />}</Field>{tooLong ? <p className="text-small text-bad">Keep the note under 16 KiB.</p> : null}</>}
          {task.treeStatus !== null && task.treeStatus !== "open" ? <Alert>The tree stays closed. Reopen the tree separately to resume scheduling.</Alert> : null}
          <section className="border-t border-line pt-3"><h3 className="label-caps mb-2">Dependent tasks</h3>
            {p.dependents.length === 0 ? <p className="text-small text-muted">No dependent tasks.</p> : <ul className="divide-y divide-line">{p.dependents.map((d) => <li key={d.taskId} className="py-2 text-small"><Link to={`/tasks/${d.taskId}`} className="underline">#{d.taskId} {d.title}</Link><p className="text-muted">{d.claimed ? "Already claimed or run. Existing work and accepted inputs stay unchanged." : reopen ? "Blocked until this task is completed again." : d.startMode === "auto" ? "Starts automatically when every prerequisite is completed and the tree is open." : "Becomes ready when every prerequisite is completed; you start it."}</p></li>)}</ul>}
          </section>
          {!reopen && p.blockedReason !== "" ? <Alert>{p.blockedReason}</Alert> : null}
        </>}
      </QueryState>
      <ErrorText error={change.error} />
      <Button variant="secondary" busy={preview.isFetching} disabled={change.isPending} onClick={() => { change.reset(); void preview.refetch(); }}>Refresh evidence</Button>
    </div>
  </Drawer>;
}
