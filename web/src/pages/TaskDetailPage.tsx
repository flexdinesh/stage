import { useMutation } from "@tanstack/react-query";
import { useRef, useState, type FormEvent, type ReactNode, type Ref } from "react";
import { Link, useNavigate } from "react-router";
import { api } from "../api/client.ts";
import { useChain, useInvalidateWork, useTask } from "../api/queries.ts";
import { SEND_MODES, type SendMode, type TaskDetail, type TaskEvent, type Thread } from "../api/types.ts";
import { CopyButton } from "../components/CopyButton.tsx";
import { IconArrowRight, IconExternal } from "../components/icons.tsx";
import { TaskStateBadge, ThreadStatusBadge, taskStateLabel } from "../components/StateBadge.tsx";
import { TaskFormModal } from "../components/TaskForm.tsx";
import {
  Alert,
  Button,
  Chip,
  Detail,
  Details,
  ErrorText,
  ExternalButton,
  Muted,
  PageHeader,
  Panel,
  QueryState,
  Segmented,
  Textarea,
  buttonClass,
  cx,
} from "../components/ui.tsx";
import { dateTime, modelText, relativeTime, workspaceText } from "../lib/format.ts";
import { useIdParam } from "../lib/params.ts";
import { canStart, canStop, isActive } from "../lib/tasks.ts";
import { NotFoundPage } from "./NotFoundPage.tsx";

export function TaskDetailPage() {
  const id = useIdParam();
  return id === null ? <NotFoundPage /> : <TaskDetailView id={id} />;
}

function TaskDetailView({ id }: { id: number }) {
  const task = useTask(id);
  return (
    <QueryState isPending={task.isPending} error={task.error}>
      {task.data === undefined ? null : <TaskBody task={task.data} />}
    </QueryState>
  );
}

const when = (iso: string | null) => (iso === null ? "-" : <span title={dateTime(iso)}>{relativeTime(iso)}</span>);

const SEND_OPTIONS = SEND_MODES.map((m) => ({ value: m, label: m }));

/** "10:42:03" today, "Nov 3 10:42" otherwise. */
function stamp(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const time = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
  if (d.toDateString() === new Date().toDateString()) return time;
  const day = d.toLocaleDateString([], { month: "short", day: "numeric" });
  return `${day} ${time.slice(0, 5)}`;
}

function workspaceChip(task: TaskDetail): string {
  const w = task.workspace;
  if (w.type === "root") return "project root";
  const branch = w.branch ?? task.thread?.branch ?? null;
  return branch === null ? `worktree from ${w.baseRef}` : `worktree ${branch}`;
}

const linkClass = "text-fg underline-offset-2 hover:underline";

function ChainMeta({ chainId, position }: { chainId: number; position: number | null }) {
  const chain = useChain(chainId);
  return (
    <>
      {" · chain "}
      <Link to={`/chains/${chainId}`} className={linkClass}>
        {chain.data?.title ?? `#${chainId}`}
      </Link>
      {position === null ? null : ` step ${position}`}
    </>
  );
}

function TaskBody({ task }: { task: TaskDetail }) {
  const navigate = useNavigate();
  const invalidate = useInvalidateWork();
  const [editing, setEditing] = useState(false);
  const followupInput = useRef<HTMLTextAreaElement>(null);
  const start = useMutation({ mutationFn: () => api.startTask(task.id), onSettled: invalidate });
  const stop = useMutation({ mutationFn: () => api.stopTask(task.id), onSettled: invalidate });
  const duplicate = useMutation({
    mutationFn: () => api.duplicateTask(task.id),
    onSuccess: async (t) => {
      await invalidate();
      await navigate(`/tasks/${t.id}`);
    },
  });
  const remove = useMutation({
    mutationFn: () => api.deleteTask(task.id),
    onSuccess: async () => {
      await navigate("/");
      await invalidate();
    },
  });
  const actionError = start.error ?? stop.error ?? duplicate.error ?? remove.error;

  const pending = task.thread?.pendingRequestCount ?? 0;
  const waiting = task.state === "needs_input" || pending > 0;
  const hasThread = task.t3ThreadId !== null;
  const startable = canStart(task);
  const stoppable = canStop(task);
  const webUrl = task.t3WebUrl;
  const phoneBar = webUrl !== null || hasThread || stoppable;

  const focusFollowup = () => {
    const el = followupInput.current;
    if (el === null) return;
    el.scrollIntoView({ behavior: "smooth", block: "center" });
    el.focus({ preventScroll: true });
  };

  const openInT3 = (variant: "primary" | "secondary", className?: string) =>
    webUrl === null ? null : (
      <ExternalButton href={webUrl} variant={variant} {...(className === undefined ? {} : { className })}>
        Open in T3 <IconExternal />
      </ExternalButton>
    );

  const stopButton = (className?: string) => (
    <Button busy={stop.isPending} onClick={() => stop.mutate()} className={className}>
      Stop
    </Button>
  );

  return (
    <div className={cx(phoneBar && "pb-16 md:pb-0")}>
      <PageHeader
        title={task.title}
        meta={
          <>
            <Link to={`/projects/${task.projectId}`} className={linkClass}>
              {task.projectTitle}
            </Link>
            {` / task ${task.id}`}
            {task.chainId === null ? null : <ChainMeta chainId={task.chainId} position={task.chainPosition} />}
          </>
        }
      />
      <div className="-mt-1 mb-3 flex flex-wrap gap-1.5">
        <TaskStateBadge state={task.state} />
        <Chip>{modelText(task.model, task.modelOptions)}</Chip>
        <Chip>{task.runtimeMode}</Chip>
        {task.interactionMode === "plan" ? <Chip>plan mode</Chip> : null}
        <Chip>{workspaceChip(task)}</Chip>
        {task.tags.map((t) => (
          <Chip key={t}>#{t}</Chip>
        ))}
      </div>

      <div className="flex flex-col gap-3.5">
        {waiting ? (
          <Alert tone="need" actions={webUrl === null ? undefined : openInT3("primary", "max-md:hidden")}>
            {pending > 0 ? `T3 is waiting on ${pending} request${pending === 1 ? "" : "s"}` : "T3 is waiting on input"}
          </Alert>
        ) : null}

        <div className="flex flex-wrap items-center gap-2">
          {startable ? (
            <Button variant="primary" busy={start.isPending} onClick={() => start.mutate()}>
              {task.state === "backlog" ? "Start" : "Restart"}
            </Button>
          ) : null}
          {waiting ? null : openInT3("secondary", "max-md:hidden")}
          {hasThread ? (
            <Button onClick={focusFollowup} className="max-md:hidden">
              Follow-up
            </Button>
          ) : null}
          {stoppable ? stopButton("max-md:hidden") : null}
          <Button busy={duplicate.isPending} onClick={() => duplicate.mutate()}>
            Duplicate
          </Button>
          <Button onClick={() => setEditing(true)}>Edit</Button>
          <Button
            variant="danger"
            className="ml-auto"
            disabled={isActive(task)}
            title={isActive(task) ? "Stop the task first" : undefined}
            busy={remove.isPending}
            onClick={() => {
              if (window.confirm(`Delete task "${task.title}"?`)) remove.mutate();
            }}
          >
            Delete
          </Button>
        </div>

        <ErrorText error={actionError} />
        {task.lastError === null ? null : (
          <Alert tone="bad">
            <span className="font-medium">Last error:</span> {task.lastError}
          </Alert>
        )}

        <div className="grid grid-cols-1 gap-3.5 lg:grid-cols-[minmax(0,1fr)_340px]">
          <div className="flex min-w-0 flex-col gap-3.5">
            <Panel title="Prompt" actions={<CopyButton text={task.prompt} label="Copy" />}>
              <pre className="max-h-96 overflow-auto font-mono text-[12.5px] leading-relaxed whitespace-pre-wrap">
                {task.prompt}
              </pre>
            </Panel>
            {hasThread ? <FollowupPanel taskId={task.id} inputRef={followupInput} /> : null}
            <TimelinePanel events={task.events} />
          </div>
          <div className="flex min-w-0 flex-col gap-3.5">
            <ThreadPanel task={task} thread={task.thread} />
            <ConfigPanel task={task} />
            <NotesPanel task={task} />
          </div>
        </div>
      </div>

      {phoneBar ? (
        <div className="fixed inset-x-0 bottom-[calc(3.5rem+env(safe-area-inset-bottom))] z-30 flex gap-2 border-t border-line bg-bg px-3 py-2.5 md:hidden">
          {openInT3(waiting ? "primary" : "secondary", "flex-1")}
          {hasThread ? (
            <Button onClick={focusFollowup} className="flex-1">
              Follow-up
            </Button>
          ) : null}
          {stoppable ? stopButton("flex-1") : null}
        </div>
      ) : null}

      {editing ? <TaskFormModal task={task} onClose={() => setEditing(false)} /> : null}
    </div>
  );
}

function ThreadPanel({ task, thread }: { task: TaskDetail; thread: Thread | null }) {
  if (task.t3ThreadId === null) {
    return (
      <Panel title="Thread">
        <Muted>No T3 thread yet. Start the task to launch one.</Muted>
      </Panel>
    );
  }
  const links: ReactNode[] = [];
  if (task.t3WebUrl !== null) {
    links.push(
      <ExternalButton key="web" href={task.t3WebUrl} small>
        Open in T3 <IconExternal />
      </ExternalButton>,
    );
  }
  if (task.t3AppUrl !== null) {
    links.push(
      <a key="app" href={task.t3AppUrl} className={buttonClass("secondary", true)}>
        Open in app
      </a>,
      <CopyButton key="copy" text={task.t3AppUrl} label="Copy app link" />,
    );
  }
  return (
    <Panel title="Thread" meta={thread === null ? undefined : <>synced {when(thread.syncedAt)}</>}>
      <Details>
        {thread === null ? null : (
          <>
            <Detail label="Status">
              <ThreadStatusBadge status={thread.status} settled={thread.settled} />
            </Detail>
            <Detail label="Pending">
              {thread.pendingRequestCount} request{thread.pendingRequestCount === 1 ? "" : "s"}
            </Detail>
            <Detail label="Settled">
              {thread.settled ? (
                <span>
                  yes{thread.settledAt === null ? null : <span className="text-muted"> ({when(thread.settledAt)})</span>}
                </span>
              ) : (
                "no (settle in T3 to finish)"
              )}
            </Detail>
            <Detail label="Model">{thread.model ?? "-"}</Detail>
            <Detail label="Branch">{thread.branch ?? "-"}</Detail>
            <Detail label="Worktree">{thread.worktreePath ?? "-"}</Detail>
            <Detail label="Synced">{when(thread.syncedAt)}</Detail>
          </>
        )}
        <Detail label="Thread id">
          <span className="flex min-w-0 items-center gap-2">
            <span className="min-w-0 truncate" title={task.t3ThreadId}>
              {task.t3ThreadId}
            </span>
            <CopyButton text={task.t3ThreadId} label="Copy" />
          </span>
        </Detail>
      </Details>
      {links.length === 0 ? null : <div className="mt-3 flex flex-wrap gap-1.5">{links}</div>}
    </Panel>
  );
}

function ConfigPanel({ task }: { task: TaskDetail }) {
  return (
    <Panel title="Config">
      <Details>
        <Detail label="Provider">{task.providerInstanceId ?? "T3 default"}</Detail>
        <Detail label="Model">{modelText(task.model, task.modelOptions)}</Detail>
        <Detail label="Runtime">{task.runtimeMode}</Detail>
        <Detail label="Interaction">{task.interactionMode}</Detail>
        <Detail label="Workspace">{workspaceText(task.workspace)}</Detail>
        <Detail label="Attempts">{task.launchAttempt}</Detail>
        <Detail label="Created">{when(task.createdAt)}</Detail>
        <Detail label="Launched">{when(task.launchedAt)}</Detail>
        <Detail label="Finished">{when(task.finishedAt)}</Detail>
        <Detail label="Settled">{when(task.settledAt)}</Detail>
      </Details>
    </Panel>
  );
}

function TimelinePanel({ events }: { events: TaskEvent[] }) {
  return (
    <Panel title="Timeline" meta={`${events.length} event${events.length === 1 ? "" : "s"}`} flush>
      {events.length === 0 ? (
        <div className="p-3">
          <Muted>No events yet. State changes appear here once the task moves.</Muted>
        </div>
      ) : (
        <ol>
          {events.map((e) => (
            <li
              key={e.id}
              className="grid grid-cols-[auto_1fr] items-center gap-x-3 border-b border-line px-3 py-1.5 last:border-b-0 sm:grid-cols-[88px_1fr]"
            >
              <span className="font-mono text-[11.5px] text-muted tabular" title={dateTime(e.at)}>
                {stamp(e.at)}
              </span>
              <span className="flex min-w-0 flex-wrap items-center gap-1.5">
                {e.fromState === null || e.fromState === e.toState ? null : (
                  <span className="inline-flex items-center gap-1.5 font-mono text-[11.5px] text-muted">
                    {taskStateLabel(e.fromState)}
                    <IconArrowRight size={12} label="to" />
                  </span>
                )}
                <TaskStateBadge state={e.toState} />
                {e.detail === "" ? null : (
                  <span className="min-w-0 font-mono text-[11.5px] break-words text-muted">{e.detail}</span>
                )}
              </span>
            </li>
          ))}
        </ol>
      )}
    </Panel>
  );
}

function NotesPanel({ task }: { task: TaskDetail }) {
  const invalidate = useInvalidateWork();
  const [draft, setDraft] = useState<string | null>(null);
  const save = useMutation({
    mutationFn: (notes: string) => api.updateTask(task.id, { notes }),
    onSuccess: async () => {
      await invalidate();
      setDraft(null);
    },
  });

  return (
    <Panel
      title="Notes"
      actions={
        draft === null ? (
          <Button small onClick={() => setDraft(task.notes)}>
            Edit
          </Button>
        ) : (
          <>
            <Button small onClick={() => setDraft(null)}>
              Cancel
            </Button>
            <Button small variant="primary" busy={save.isPending} onClick={() => save.mutate(draft)}>
              Save
            </Button>
          </>
        )
      }
    >
      {draft === null ? (
        task.notes === "" ? (
          <Muted>No notes. Use Edit to jot context for later.</Muted>
        ) : (
          <p className="text-[12.5px] whitespace-pre-wrap">{task.notes}</p>
        )
      ) : (
        <div className="flex flex-col gap-2">
          <Textarea rows={4} value={draft} onChange={(e) => setDraft(e.target.value)} autoFocus aria-label="Notes" />
          <ErrorText error={save.error} />
        </div>
      )}
    </Panel>
  );
}

function FollowupPanel({ taskId, inputRef }: { taskId: number; inputRef: Ref<HTMLTextAreaElement> }) {
  const invalidate = useInvalidateWork();
  const [message, setMessage] = useState("");
  const [mode, setMode] = useState<SendMode>("auto");
  const send = useMutation({
    mutationFn: () => api.followupTask(taskId, { message, mode }),
    onSuccess: async () => {
      setMessage("");
      await invalidate();
    },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (message.trim() !== "") send.mutate();
  };

  return (
    <Panel title="Follow-up">
      <form onSubmit={submit} className="flex flex-col gap-2.5">
        <Textarea
          ref={inputRef}
          rows={3}
          value={message}
          onChange={(e) => setMessage(e.target.value)}
          placeholder="Message to send to the thread"
          aria-label="Follow-up message"
        />
        <div className="flex flex-wrap items-center gap-2.5">
          <Segmented options={SEND_OPTIONS} value={mode} onChange={setMode} label="Send mode" small />
          <Button
            type="submit"
            variant="primary"
            small
            className="ml-auto"
            busy={send.isPending}
            disabled={message.trim() === ""}
          >
            Send
          </Button>
        </div>
        <ErrorText error={send.error} />
      </form>
    </Panel>
  );
}
