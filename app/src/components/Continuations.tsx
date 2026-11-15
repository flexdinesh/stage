import { useMutation } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { useApi } from "../platform/context.tsx";
import { useContinuations, useInvalidateWork, useThreads } from "../api/queries.ts";
import type { Continuation, Thread } from "../api/types.ts";
import { dateTime, relativeTime } from "../lib/format.ts";
import { canEditContinuation, canLinkSession, CONTINUATION_LABELS, CONTINUATION_TONES, continuationPreview, isPendingContinuation, sessionPath, WAIT_REASONS } from "../lib/sessions.ts";
import { ProjectSelect } from "./ProjectSelect.tsx";
import { SessionPager } from "./SessionList.tsx";
import { TaskStateBadge } from "./StateBadge.tsx";
import { Alert, Button, Chip, Detail, Details, Drawer, ErrorText, Field, Input, Panel, QueryState, Textarea } from "./ui.tsx";

export function ContinuationBadge({ entry }: { entry: Continuation }) {
  return <Chip dot tone={CONTINUATION_TONES[entry.state]}>{CONTINUATION_LABELS[entry.state]}</Chip>;
}

export function ContinuationWait({ entry }: { entry: Continuation }) {
  if (entry.waitReasons.length > 0) return <ul className="flex flex-col gap-1 text-small text-muted">{entry.waitReasons.map((reason) => <li key={reason}>{WAIT_REASONS[reason]}</li>)}</ul>;
  const copy: Record<Continuation["state"], string> = {
    waiting: "Waiting for readiness. The predecessor must settle and the target must finish with no pending input.",
    paused: "Paused. Resume to check readiness again.", blocked: "Blocked. Review the linked sessions before resuming.",
    sending: "Delivery claimed. Changes are locked while delivery is confirmed.",
    running: "The queued prompt was delivered. Its resulting run is in progress.",
    awaiting_settle: "The continuation run finished. Settle its target session in T3 to release the next step.",
    settled: "Continuation finished and settled.", cancelled: "Cancelled. Downstream continuations stay blocked.",
    uncertain: "Delivery could not be confirmed. Review the target session in T3. No automatic retry.",
  };
  return <p className="text-small text-muted">{copy[entry.state]}</p>;
}

function SessionEndpoint({ thread, label }: { thread: Thread; label: string }) {
  return <div className="flex min-w-0 flex-col gap-1">
    <span className="text-small text-muted">{label}</span>
    <Link to={sessionPath(thread.t3ThreadId)} className="text-body font-medium break-words hover:underline">{thread.title}</Link>
    <div className="flex flex-wrap items-center gap-2 font-mono text-meta text-muted"><span>{thread.projectTitle ?? thread.t3ProjectId}</span><TaskStateBadge state={thread.state} /></div>
  </div>;
}

export function RunOrderPanel({ thread, entries }: { thread: Thread; entries: Continuation[] }) {
  const [queue, setQueue] = useState(false);
  const [editing, setEditing] = useState<number | null>(null);
  const editEntry = entries.find((entry) => entry.id === editing);
  return <>
    <Panel title="Run order" flush actions={canLinkSession(thread) ? <Button small onClick={() => setQueue(true)}>Queue continuation</Button> : undefined}>
      {entries.length === 0 ? <p className="p-3 text-small text-muted">{canLinkSession(thread) ? "Queue a saved prompt for an existing session after this session settles. Current runs continue unchanged." : "Child and archived sessions can be inspected here. Queue continuations from a top-level, unarchived session."}</p> :
        <ol>{entries.map((entry) => <li key={entry.id} className="border-b border-line p-3 last:border-b-0"><ContinuationRow entry={entry} onEdit={() => setEditing(entry.id)} /></li>)}</ol>}
    </Panel>
    {queue ? <ContinuationDrawer after={thread} onClose={() => setQueue(false)} /> : null}
    {editEntry === undefined ? null : <ContinuationDrawer key={editEntry.id} after={editEntry.afterThread} entry={editEntry} onClose={() => setEditing(null)} />}
  </>;
}

function ContinuationRow({ entry, onEdit }: { entry: Continuation; onEdit: () => void }) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const [cancelArmed, setCancelArmed] = useState(false);
  const action = useMutation({ mutationFn: (name: "pause" | "resume" | "cancel") => api.continuationAction(entry.id, name), onSettled: invalidate });
  const editable = canEditContinuation(entry);
  return <div className="flex flex-col gap-3">
    <div className="flex flex-wrap items-center gap-2"><span className="font-mono text-meta text-muted">Continuation #{entry.id}</span><ContinuationBadge entry={entry} /></div>
    <div className="grid gap-3 md:grid-cols-2"><SessionEndpoint thread={entry.afterThread} label="After session" /><SessionEndpoint thread={entry.targetThread} label="Deliver to session" /></div>
    <ContinuationWait entry={entry} />
    <ErrorText error={entry.lastError} />
    <details>
      <summary className="cursor-pointer text-small font-medium">Saved prompt and delivery preview</summary>
      <pre className="mt-2 max-h-80 overflow-y-auto border border-line bg-panel p-3 font-mono text-small leading-relaxed break-words whitespace-pre-wrap">{continuationPreview(entry.prompt, entry.afterThread)}</pre>
      <p className="mt-1 text-meta text-muted">The saved prompt is preserved verbatim. Only the preceding session reference is appended.</p>
    </details>
    <details>
      <summary className="cursor-pointer text-small text-muted">Delivery history</summary>
      <div className="mt-2"><Details>
        <Detail label="Saved">{dateTime(entry.createdAt)}</Detail><Detail label="Updated">{dateTime(entry.updatedAt)}</Detail>
        <Detail label="Delivered">{entry.sentAt === null ? "Not confirmed" : dateTime(entry.sentAt)}</Detail>
        <Detail label="Settled">{entry.settledAt === null ? "Not settled" : dateTime(entry.settledAt)}</Detail>
        <Detail label="Run">{entry.runId ?? "Not recorded"}</Detail><Detail label="Message">{entry.messageId ?? "Not recorded"}</Detail>
        {entry.previousContinuationId === null ? null : <Detail label="After continuation">#{entry.previousContinuationId}</Detail>}
      </Details></div>
    </details>
    {editable ? <div className="flex flex-wrap items-center gap-2">
      <Button small disabled={action.isPending} onClick={onEdit}>Edit prompt</Button>
      <Button small busy={action.isPending && action.variables !== "cancel"} disabled={action.isPending} onClick={() => action.mutate(entry.state === "waiting" ? "pause" : "resume")}>{entry.state === "waiting" ? "Pause" : "Resume"}</Button>
      {entry.state === "blocked" ? <Button small disabled={action.isPending} onClick={() => action.mutate("pause")}>Pause</Button> : null}
      <Button small variant="danger" busy={action.isPending && action.variables === "cancel"} disabled={action.isPending} onClick={() => cancelArmed ? action.mutate("cancel") : setCancelArmed(true)}>{cancelArmed ? "Confirm cancel" : "Cancel continuation"}</Button>
      {cancelArmed ? <><span className="text-small text-muted">Downstream steps will stay blocked.</span><Button small variant="ghost" onClick={() => setCancelArmed(false)}>Keep continuation</Button></> : null}
    </div> : null}
    {entry.state === "uncertain" ? <Alert tone="bad">Review delivery in the target session. Resume cannot safely retry an unconfirmed send.</Alert> : null}
    <ErrorText error={action.error} />
  </div>;
}

const PICKER_SIZE = 12;

function TargetPicker({ after, selected, onSelect }: { after: Thread; selected: Thread | null; onSelect: (thread: Thread | null) => void }) {
  const [q, setQ] = useState("");
  const [project, setProject] = useState("");
  const [page, setPage] = useState(0);
  const orders = useContinuations();
  const pending = (orders.data?.continuations ?? []).filter(isPendingContinuation);
  const threads = useThreads({ subagents: "exclude", sort: "activity", q, limit: PICKER_SIZE, offset: page * PICKER_SIZE, ...(project === "" ? {} : { project: Number(project) }) });
  return <div className="flex flex-col gap-3">
    {selected === null ? null : <div className="flex items-start gap-3 border border-line bg-panel p-3"><div className="min-w-0 flex-1"><SessionEndpoint thread={selected} label="Selected target" /><p className="mt-1 break-all font-mono text-meta text-muted">{selected.t3ThreadId}</p></div><Button small onClick={() => onSelect(null)}>Change</Button></div>}
    {selected === null ? <>
      <Field label="Find target session">{(id) => <Input id={id} data-autofocus type="search" placeholder="Search session titles" value={q} onChange={(event) => { setQ(event.target.value); setPage(0); }} />}</Field>
      <Field label="Target project">{(id) => <ProjectSelect id={id} value={project} onChange={(value) => { setProject(value); setPage(0); }} />}</Field>
      <QueryState isPending={threads.isPending} error={threads.error}>
        {threads.data === undefined ? null : <>
          <div role="group" aria-label="Target sessions" className="max-h-80 overflow-y-auto border border-line">
            {threads.data.threads.length === 0 ? <p className="p-3 text-small text-muted">No sessions match. Try another title or project.</p> : threads.data.threads.map((thread) => {
              const incoming = pending.find((entry) => entry.targetThreadId === thread.t3ThreadId);
              const outgoing = pending.find((entry) => entry.afterThreadId === thread.t3ThreadId);
              const eligible = canLinkSession(thread) && thread.t3ThreadId !== after.t3ThreadId && incoming === undefined;
              return <button key={thread.t3ThreadId} type="button" disabled={!eligible || threads.isPlaceholderData} onClick={() => onSelect(thread)} className="flex w-full flex-col gap-1.5 border-b border-line px-3 py-2.5 text-left last:border-b-0 hover:bg-panel disabled:opacity-55">
                <span className="text-body font-medium break-words">{thread.title}</span>
                <span className="flex flex-wrap items-center gap-2 font-mono text-meta text-muted"><span>{thread.projectTitle ?? thread.t3ProjectId}</span><TaskStateBadge state={thread.state} /><span>{relativeTime(thread.updatedAt)}</span></span>
                <span className="break-all font-mono text-meta-sm text-muted">{thread.t3ThreadId}</span>
                {outgoing === undefined ? null : <span className="text-small text-muted">Then continues {outgoing.targetThread.title}</span>}
                {eligible ? null : <span className="text-small text-muted">{thread.t3ThreadId === after.t3ThreadId ? "Cannot run after itself" : incoming === undefined ? "Archived or child session" : `Already queued after ${incoming.afterThread.title}`}</span>}
              </button>;
            })}
          </div>
          <SessionPager page={page} size={PICKER_SIZE} total={threads.data.total} disabled={threads.isPlaceholderData} onPage={setPage} />
        </>}
      </QueryState>
    </> : null}
  </div>;
}

function ContinuationDrawer({ after, entry, onClose }: { after: Thread; entry?: Continuation; onClose: () => void }) {
  const api = useApi();
  const [target, setTarget] = useState<Thread | null>(entry?.targetThread ?? null);
  const [prompt, setPrompt] = useState(entry?.prompt ?? "");
  const invalidate = useInvalidateWork();
  const save = useMutation({
    mutationFn: () => {
      if (entry !== undefined) return api.updateContinuation(entry.id, prompt);
      if (target === null) throw new Error("Select a target session.");
      return api.createContinuation({ afterThreadId: after.t3ThreadId, targetThreadId: target.t3ThreadId, prompt });
    },
    onSuccess: async () => { await invalidate(); onClose(); }, onError: invalidate,
  });
  const locked = entry !== undefined && !canEditContinuation(entry);
  const submit = (event: FormEvent) => { event.preventDefault(); if (!locked && target !== null && prompt.trim() !== "") save.mutate(); };
  return <Drawer title={entry === undefined ? "Queue continuation" : "Edit continuation"} onClose={onClose} wide footer={<><Button onClick={onClose}>Close</Button><Button form="continuation-form" type="submit" variant="primary" className="ml-auto" disabled={locked || target === null || prompt.trim() === ""} busy={save.isPending}>{entry === undefined ? "Save continuation" : "Save prompt"}</Button></>}>
    <form id="continuation-form" onSubmit={submit} className="flex flex-col gap-5">
      <p className="text-small text-muted">Save a prompt for an existing session. Saving sends nothing and leaves current runs untouched. Delivery waits for the predecessor to settle and the target to finish with no pending input.</p>
      <SessionEndpoint thread={after} label="After this session settles" />
      {entry === undefined ? <TargetPicker after={after} selected={target} onSelect={setTarget} /> : <SessionEndpoint thread={entry.targetThread} label="Deliver to session" />}
      <Field label="Saved prompt" hint="Session reference is appended automatically. No transcript is copied.">{(id) => <Textarea id={id} data-autofocus={entry === undefined ? undefined : true} rows={7} required disabled={locked} value={prompt} onChange={(event) => setPrompt(event.target.value)} placeholder="What should the target session do next?" />}</Field>
      <div className="flex flex-col gap-2"><h3 className="text-small font-medium">Delivery preview</h3><pre className="max-h-80 overflow-y-auto border border-line bg-panel p-3 font-mono text-small leading-relaxed break-words whitespace-pre-wrap">{continuationPreview(prompt, after)}</pre></div>
      {locked ? <Alert tone="bad">Delivery has been claimed or this continuation is no longer editable. Close to review its current state.</Alert> : null}
      <ErrorText error={save.error} />
    </form>
  </Drawer>;
}

export function PendingContinuationsPanel({ project }: { project?: number | undefined }) {
  const query = useContinuations();
  const entries = (query.data?.continuations ?? []).filter((entry) => isPendingContinuation(entry) && (project === undefined || entry.afterThread.projectId === project || entry.targetThread.projectId === project));
  if (entries.length === 0) return query.error === null ? null : <ErrorText error={query.error} />;
  return <Panel title="Queued continuations" className="mb-3.5" meta={`${entries.length} pending`} flush>
    <ul>{entries.map((entry) => <li key={entry.id} className="flex flex-col gap-2 border-b border-line p-3 last:border-b-0">
      <div className="flex flex-wrap items-center gap-2"><Link to={sessionPath(entry.afterThreadId)} className="text-small font-medium hover:underline">{entry.afterThread.title}</Link><span className="text-small text-muted">then</span><Link to={sessionPath(entry.targetThreadId)} className="text-small font-medium hover:underline">{entry.targetThread.title}</Link><ContinuationBadge entry={entry} /></div>
      <ContinuationWait entry={entry} />
    </li>)}</ul>
  </Panel>;
}
