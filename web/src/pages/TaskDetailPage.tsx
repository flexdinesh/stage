import { useMutation } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router";
import { api } from "../api/client.ts";
import { useInvalidateWork, useTask } from "../api/queries.ts";
import { SEND_MODES, type SendMode, type TaskDetail, type Thread } from "../api/types.ts";
import { CopyButton } from "../components/CopyButton.tsx";
import { TaskStateBadge, ThreadStatusBadge } from "../components/StateBadge.tsx";
import { TaskFormModal } from "../components/TaskForm.tsx";
import {
  Badge,
  Button,
  Card,
  Detail,
  Details,
  ErrorText,
  ExternalButton,
  Field,
  Muted,
  PageHeader,
  QueryState,
  Select,
  Textarea,
  buttonClass,
} from "../components/ui.tsx";
import { dateTime, modelText, pick, relativeTime, workspaceText } from "../lib/format.ts";
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

function TaskBody({ task }: { task: TaskDetail }) {
  const navigate = useNavigate();
  const invalidate = useInvalidateWork();
  const [editing, setEditing] = useState(false);
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

  return (
    <>
      <PageHeader
        title={task.title}
        subtitle={
          <span className="flex flex-wrap items-center gap-2">
            <TaskStateBadge state={task.state} />
            <span>#{task.id}</span>
            <Link to={`/projects/${task.projectId}`} className="hover:underline">
              {task.projectTitle}
            </Link>
            {task.chainId === null ? null : (
              <Link to={`/chains/${task.chainId}`} className="hover:underline">
                chain #{task.chainId}
              </Link>
            )}
            {task.tags.map((t) => (
              <Badge key={t} tone="violet">
                {t}
              </Badge>
            ))}
          </span>
        }
        actions={
          <>
            {canStart(task) ? (
              <Button variant="primary" busy={start.isPending} onClick={() => start.mutate()}>
                {task.state === "backlog" ? "Start" : "Restart"}
              </Button>
            ) : null}
            {canStop(task) ? (
              <Button variant="danger" busy={stop.isPending} onClick={() => stop.mutate()}>
                Stop
              </Button>
            ) : null}
            {task.t3WebUrl === null ? null : <ExternalButton href={task.t3WebUrl}>Open in T3</ExternalButton>}
            <Button onClick={() => setEditing(true)}>Edit</Button>
            <Button busy={duplicate.isPending} onClick={() => duplicate.mutate()}>
              Duplicate
            </Button>
            <Button
              variant="danger"
              disabled={isActive(task)}
              title={isActive(task) ? "Stop the task first" : undefined}
              busy={remove.isPending}
              onClick={() => {
                if (window.confirm(`Delete task "${task.title}"?`)) remove.mutate();
              }}
            >
              Delete
            </Button>
          </>
        }
      />
      <div className="flex flex-col gap-4">
        <ErrorText error={actionError} />
        {task.lastError === null ? null : (
          <div className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900 dark:bg-red-950 dark:text-red-300">
            <span className="font-medium">Last error:</span> {task.lastError}
          </div>
        )}
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-[3fr_2fr]">
          <div className="flex min-w-0 flex-col gap-4">
            <Card title="Prompt">
              <pre className="max-h-96 overflow-auto font-mono text-sm whitespace-pre-wrap">{task.prompt}</pre>
            </Card>
            <Card title="Configuration">
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
            </Card>
            <NotesCard task={task} />
            {task.t3ThreadId === null ? null : <FollowupCard taskId={task.id} />}
          </div>
          <div className="flex min-w-0 flex-col gap-4">
            <ThreadCard task={task} thread={task.thread} />
            <TimelineCard task={task} />
          </div>
        </div>
      </div>
      {editing ? <TaskFormModal task={task} onClose={() => setEditing(false)} /> : null}
    </>
  );
}

function ThreadCard({ task, thread }: { task: TaskDetail; thread: Thread | null }) {
  if (task.t3ThreadId === null) {
    return (
      <Card title="Thread">
        <Muted>No T3 thread yet.</Muted>
      </Card>
    );
  }
  return (
    <Card
      title="Thread"
      actions={
        task.t3AppUrl === null ? null : (
          <>
            <a href={task.t3AppUrl} className={buttonClass("secondary", true)}>
              Open in app
            </a>
            <CopyButton text={task.t3AppUrl} label="Copy app link" />
          </>
        )
      }
    >
      <Details>
        {thread === null ? null : (
          <>
            <Detail label="Status">
              <ThreadStatusBadge status={thread.status} />
            </Detail>
            <Detail label="Settled">
              {thread.settled ? (
                <span>
                  yes {thread.settledAt === null ? null : <span className="text-neutral-500">({when(thread.settledAt)})</span>}
                </span>
              ) : (
                "no (settle in T3 to finish)"
              )}
            </Detail>
            <Detail label="Pending requests">{thread.pendingRequestCount}</Detail>
            <Detail label="Model">{thread.model ?? "-"}</Detail>
            <Detail label="Branch">{thread.branch ?? "-"}</Detail>
            <Detail label="Worktree">
              <span className="font-mono text-xs">{thread.worktreePath ?? "-"}</span>
            </Detail>
            <Detail label="Synced">{when(thread.syncedAt)}</Detail>
          </>
        )}
        <Detail label="Thread id">
          <span className="font-mono text-xs">{task.t3ThreadId}</span>
        </Detail>
      </Details>
    </Card>
  );
}

function TimelineCard({ task }: { task: TaskDetail }) {
  return (
    <Card title="Timeline">
      {task.events.length === 0 ? (
        <Muted>No events.</Muted>
      ) : (
        <ol className="flex flex-col gap-2 border-l border-neutral-200 pl-4 dark:border-neutral-800">
          {task.events.map((e) => (
            <li key={e.id} className="relative text-sm">
              <span className="absolute top-1.5 -left-[1.3rem] size-2 rounded-full bg-neutral-400" />
              <div className="flex flex-wrap items-center gap-1.5">
                {e.fromState === null ? null : (
                  <>
                    <TaskStateBadge state={e.fromState} />
                    <span className="text-neutral-400">→</span>
                  </>
                )}
                <TaskStateBadge state={e.toState} />
                <span className="text-xs text-neutral-500" title={dateTime(e.at)}>
                  {relativeTime(e.at)}
                </span>
              </div>
              {e.detail === "" ? null : <div className="text-xs text-neutral-500">{e.detail}</div>}
            </li>
          ))}
        </ol>
      )}
    </Card>
  );
}

function NotesCard({ task }: { task: TaskDetail }) {
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
    <Card
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
          <Muted>No notes.</Muted>
        ) : (
          <p className="text-sm whitespace-pre-wrap">{task.notes}</p>
        )
      ) : (
        <div className="flex flex-col gap-2">
          <Textarea rows={4} value={draft} onChange={(e) => setDraft(e.target.value)} autoFocus aria-label="Notes" />
          <ErrorText error={save.error} />
        </div>
      )}
    </Card>
  );
}

function FollowupCard({ taskId }: { taskId: number }) {
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
    <Card title="Follow-up">
      <form onSubmit={submit} className="flex flex-col gap-3">
        <Textarea
          rows={4}
          value={message}
          onChange={(e) => setMessage(e.target.value)}
          placeholder="Message to send to the thread"
          aria-label="Follow-up message"
        />
        <div className="flex flex-wrap items-end justify-between gap-3">
          <Field label="Mode">
            {(id) => (
              <Select id={id} value={mode} onChange={(e) => setMode(pick(SEND_MODES, e.target.value) ?? mode)}>
                {SEND_MODES.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          <Button type="submit" variant="primary" busy={send.isPending} disabled={message.trim() === ""}>
            Send
          </Button>
        </div>
        <ErrorText error={send.error} />
      </form>
    </Card>
  );
}
