import { batchPath, sessionPath } from "../lib/sessions.ts";
import { useMutation } from "@tanstack/react-query";
import { useEffect, useRef, useState, type FormEvent, type ReactNode, type Ref } from "react";
import { Link, useNavigate } from "react-router";
import { useApi, usePlatform } from "../platform/context.tsx";
import { useInvalidateWork, useTask } from "../api/queries.ts";
import { SEND_MODES, type SendMode, type TaskDetail, type TaskEvent, type Thread } from "../api/types.ts";
import { CopyButton } from "../components/CopyButton.tsx";
import { CopyLinkPair } from "../components/StageLinks.tsx";
import { HandoffRecord } from "../components/Handoff.tsx";
import { IconArrowRight } from "../components/icons.tsx";
import { LastWordPanel } from "../components/LastWord.tsx";
import { OpeningPrompt, PromptPanel } from "../components/PromptPanel.tsx";
import { TaskStateBadge, ThreadStatusBadge, taskStateLabel } from "../components/StateBadge.tsx";
import { ImageDropzone, ImageStrip, useImageSet } from "../components/Images.tsx";
import { PullRequestsPanel } from "../components/PullRequests.tsx";
import { RunsPanel } from "../components/RunsPanel.tsx";
import { NeedsInputSlot, focusAnswerPanel } from "../components/NeedsInputPanel.tsx";
import { BatchPanel } from "../components/CreatedTasks.tsx";
import { NodeChip } from "../components/trees/TreeNode.tsx";
import { NoTreePanel, TreePanel } from "../components/trees/TreePanel.tsx";
import { SnippetTextarea } from "../components/SnippetPicker.tsx";
import { TaskFormModal } from "../components/TaskForm.tsx";
import { DetailViewTabs, Transcript, isLiveState, useDetailView } from "../components/TranscriptView.tsx";
import {
  Alert,
  AppButton,
  Button,
  Chip,
  Detail,
  Details,
  ErrorText,
  Muted,
  buttonClass,
  PageHeader,
  Panel,
  QueryState,
  Segmented,
  cx,
} from "../components/ui.tsx";
import { dateTime, modelText, relativeTime, workspaceText } from "../lib/format.ts";
import { pendingHandoffNote } from "../lib/handoff.ts";
import { pendingBaseNote, predecessorChip, predecessorWorkspaceText } from "../lib/launchbase.ts";
import { useIdParam } from "../lib/params.ts";
import { browserTimeZone, wallTime } from "../lib/schedules.ts";
import { useMarkNotificationGroupsRead } from "../components/Notifications.tsx";
import { canStart, canStop, isActive, isWaiting, waitTarget } from "../lib/tasks.ts";
import { NotFoundPage } from "./NotFoundPage.tsx";

export function TaskDetailPage() {
  const id = useIdParam();
  return id === null ? <NotFoundPage /> : <TaskDetailView id={id} />;
}

function TaskDetailView({ id }: { id: number }) {
  const task = useTask(id);
  // Viewing the task clears its system notification here and marks its group
  // read, so other devices close theirs on their next sync.
  const { notifications } = usePlatform();
  const markViewed = useMarkNotificationGroupsRead();
  useEffect(() => {
    void notifications?.closeShown(`task-${id}`);
    markViewed([`task:${id}`]);
  }, [notifications, id, markViewed]);
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
  if (branch !== null) return `worktree ${branch}`;
  return predecessorChip(task, task.currentLaunchBase) ?? `worktree from ${w.baseRef}`;
}

const linkClass = "text-fg underline-offset-2 hover:underline";

function TaskBody({ task }: { task: TaskDetail }) {
  const api = useApi();
  const navigate = useNavigate();
  const invalidate = useInvalidateWork();
  const [editing, setEditing] = useState(false);
  const [view, setView] = useDetailView();
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
  const archive = useMutation({
    mutationFn: (archived: boolean) => (archived ? api.archiveTask(task.id) : api.unarchiveTask(task.id)),
    onSettled: invalidate,
  });
  const actionError = start.error ?? stop.error ?? duplicate.error ?? remove.error ?? archive.error;
  const archived = task.archivedAt !== null;

  const pending = task.thread?.pendingRequestCount ?? 0;
  const waiting = task.state === "needs_input" || pending > 0;
  // Questions stage can answer: the phone bar leads with Answer.
  const answerable = waiting && task.pendingQuestionCount > 0;
  const hasThread = task.t3ThreadId !== null;
  const startable = canStart(task);
  // Blocked on its needs: Start runs it before they settle (needs kept), so it takes a second click to confirm.
  const blocked = isWaiting(task);
  const [armed, setArmed] = useState(false);
  const stoppable = canStop(task);
  const appUrl = task.t3DeepLink;
  const phoneBar = appUrl !== null || hasThread || stoppable || answerable;

  const focusFollowup = () => {
    const el = followupInput.current;
    if (el === null) return;
    el.scrollIntoView({ behavior: "smooth", block: "center" });
    el.focus({ preventScroll: true });
  };

  const openInApp = (variant: "primary" | "secondary", className?: string) =>
    appUrl === null ? null : (
      <AppButton href={appUrl} variant={variant} {...(className === undefined ? {} : { className })}>
        Open in app
      </AppButton>
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
        actions={<CopyLinkPair subject={{ kind: "task", id: task.id, title: task.title }} />}
        meta={
          <>
            <Link to={`/projects/${task.projectId}`} className={linkClass}>
              {task.projectTitle}
            </Link>
            {` / task ${task.id}`}
            {task.startAt === null ? null : (
              <span title={dateTime(task.startAt)}>{` · starts ${wallTime(task.startAt, task.startTz ?? browserTimeZone(), { zone: true })}`}</span>
            )}
            {task.scheduleId === null ? null : (
              <>
                {" · from "}
                <Link to={`/schedules/${task.scheduleId}`} className={linkClass}>
                  {task.scheduleTitle ?? `schedule #${task.scheduleId}`}
                </Link>
              </>
            )}
          </>
        }
      />
      <div className="-mt-1 mb-3 flex flex-wrap gap-1.5">
        <NodeChip task={task} />
        {archived ? <Chip title={`Archived ${dateTime(task.archivedAt ?? "")}`}>archived</Chip> : null}
        {task.adopted ? <Chip title="An existing T3 session stage tracks; it never starts it">tracked session</Chip> : null}
        <Chip>{modelText(task.model, task.modelOptions)}</Chip>
        <Chip>{task.runtimeMode}</Chip>
        {task.interactionMode === "plan" ? <Chip>plan mode</Chip> : null}
        <Chip>{workspaceChip(task)}</Chip>
        {task.tags.map((t) => (
          <Chip key={t}>#{t}</Chip>
        ))}
      </div>

      <div className="flex flex-col gap-3.5">
        {task.t3ThreadId !== null ? (
          <NeedsInputSlot pending={pending} threadId={task.t3ThreadId} links={task} />
        ) : null}
        {/* Needs input before T3 reports the request (sync lag): no questions to show yet. */}
        {waiting && pending === 0 ? (
          <Alert tone="need" actions={appUrl === null ? undefined : openInApp("primary", "max-md:hidden")}>
            T3 is waiting on input
          </Alert>
        ) : null}

        <div className="flex flex-wrap items-center gap-2">
          {startable && blocked ? (
            <Button
              variant={armed ? "primary" : "secondary"}
              busy={start.isPending}
              title={`Starts it now, before ${waitTarget(task)} settle${task.needs.length === 1 ? "s" : ""}; its needs are kept`}
              onClick={() => (armed ? start.mutate() : setArmed(true))}
              onBlur={() => setArmed(false)}
            >
              {armed ? `Start before ${waitTarget(task)} settle${task.needs.length === 1 ? "s" : ""}` : "Start"}
            </Button>
          ) : startable ? (
            <Button
              variant="primary"
              busy={start.isPending}
              onClick={() => start.mutate()}
              {...(task.startAt === null ? {} : { title: "Starts now instead of at the scheduled time" })}
            >
              {task.state !== "backlog" ? "Restart" : task.startAt !== null ? "Start now" : "Start"}
            </Button>
          ) : null}
          {waiting ? null : openInApp("secondary", "max-md:hidden")}
          {hasThread ? (
            <Button onClick={focusFollowup} className="max-md:hidden">
              Follow-up
            </Button>
          ) : null}
          {stoppable ? stopButton("max-md:hidden") : null}
          {task.adopted ? null : (
            <Button busy={duplicate.isPending} onClick={() => duplicate.mutate()}>
              Duplicate
            </Button>
          )}
          <Button onClick={() => setEditing(true)}>Edit</Button>
          {/* Archive (standalone tasks; a member closes with its tree), then Delete far right, only before it ever started. */}
          {archived ? (
            <Button className="ml-auto" busy={archive.isPending} onClick={() => archive.mutate(false)}>
              Unarchive
            </Button>
          ) : task.treeId === null ? (
            <Button
              className="ml-auto"
              disabled={isActive(task)}
              title={isActive(task) ? "Stop the task first" : "Hide it from the Dashboard and lists; activity on it unarchives it"}
              busy={archive.isPending}
              onClick={() => archive.mutate(true)}
            >
              Archive
            </Button>
          ) : null}
          {task.deletable ? (
            <Button
              variant="danger"
              className={cx(!archived && task.treeId !== null && "ml-auto")}
              disabled={isActive(task)}
              title={isActive(task) ? "Stop the task first" : undefined}
              busy={remove.isPending}
              onClick={() => {
                if (window.confirm(`Delete task "${task.title}"?`)) remove.mutate();
              }}
            >
              Delete
            </Button>
          ) : null}
        </div>

        <ErrorText error={actionError} />
        {task.lastError === null ? null : (
          <Alert tone="bad">
            <span className="font-medium">Last error:</span> {task.lastError}
          </Alert>
        )}

        {task.t3ThreadId === null ? null : <DetailViewTabs view={view} onChange={setView} live={isLiveState(task.state)} />}

        <div className="grid grid-cols-1 gap-3.5 lg:grid-cols-[minmax(0,1fr)_340px]">
          {task.t3ThreadId !== null && view === "transcript" ? (
            <div className="flex min-w-0 flex-col gap-3.5">
              <Transcript threadId={task.t3ThreadId} jumpOffset="task" />
              <FollowupPanel taskId={task.id} projectId={task.projectId} inputRef={followupInput} />
            </div>
          ) : (
          <div className="flex min-w-0 flex-col gap-3.5">
            {task.adopted && task.t3ThreadId !== null ? (
              <OpeningPrompt threadId={task.t3ThreadId} live={isLiveState(task.state)} />
            ) : (
              <PromptPanel text={task.prompt}>
                {pendingHandoffNote(task) === null ? null : (
                  <p className="font-mono text-meta text-muted">{pendingHandoffNote(task)}</p>
                )}
                {pendingBaseNote(task) === null ? null : <p className="font-mono text-meta text-muted">{pendingBaseNote(task)}</p>}
                <ImageStrip attachments={task.attachments} label="Prompt images" />
                {task.currentHandoffs.map((h) => (
                  <HandoffRecord key={`${h.attempt}:${h.sourceTaskId ?? h.sourceThreadId}`} handoff={h} />
                ))}
              </PromptPanel>
            )}
            {task.thread === null ? null : <LastWordPanel thread={task.thread} />}
            {hasThread ? <FollowupPanel taskId={task.id} projectId={task.projectId} inputRef={followupInput} /> : null}
            {task.thread === null ? null : <RunsPanel runs={task.thread.recentRuns ?? []} runCount={task.thread.runCount} />}
            <TimelinePanel events={task.events} />
          </div>
          )}
          <div className="flex min-w-0 flex-col gap-3.5">
            {task.tree === null ? <NoTreePanel task={task} /> : <TreePanel task={task} tree={task.tree} />}
            <PullRequestsPanel prs={task.pullRequests} />
            <BatchPanel task={task} />
            <ThreadPanel task={task} thread={task.thread} />
            <ConfigPanel task={task} />
            <NotesPanel task={task} />
          </div>
        </div>
      </div>

      {phoneBar ? (
        <div className="fixed inset-x-0 bottom-[calc(3.5rem+env(safe-area-inset-bottom))] z-30 flex gap-2 border-t border-line bg-bg px-3 py-2.5 md:hidden">
          {answerable ? (
            <Button variant="primary" onClick={focusAnswerPanel} className="flex-1">
              Answer
            </Button>
          ) : null}
          {openInApp(waiting && !answerable ? "primary" : "secondary", "flex-1")}
          {/* While answerable, Answer takes Follow-up's slot so the bar fits a phone. */}
          {hasThread && !answerable ? (
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
  const links: ReactNode[] = [<Link key="session" to={sessionPath(task.t3ThreadId)} className={buttonClass("secondary", true)}>View session</Link>];
  if (task.t3DeepLink !== null) {
    links.push(
      <AppButton key="app" href={task.t3DeepLink} small>
        Open in app
      </AppButton>,
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
        <Detail label="Workspace">
          {predecessorWorkspaceText(task, task.currentLaunchBase) ?? workspaceText(task.workspace)}
          {task.currentLaunchBase?.reason == null ? null : (
            <span className="block text-meta text-muted">fell back: {task.currentLaunchBase.reason}</span>
          )}
        </Detail>
        {task.startAt === null ? null : (
          <Detail label="Starts">
            <span title={dateTime(task.startAt)}>{wallTime(task.startAt, task.startTz ?? browserTimeZone(), { zone: true })}</span>
          </Detail>
        )}
        {task.needs.length === 0 ? null : (
          <Detail label="Needs">
            <span className="flex flex-wrap gap-x-2">
              {task.needs.map((n) => (
                <Link key={n} to={`/tasks/${n}`} className={linkClass}>
                  #{n}
                </Link>
              ))}
            </span>
          </Detail>
        )}
        {task.originThreadId === null ? null : (
          <Detail label="Created by">
            <Link to={batchPath(task.originThreadId)} className={linkClass}>
              {task.originThreadTitle ?? task.originThreadId}
            </Link>
          </Detail>
        )}
        {task.scheduleId === null ? null : (
          <Detail label="Schedule">
            <Link to={`/schedules/${task.scheduleId}`} className={linkClass}>
              {task.scheduleTitle ?? `#${task.scheduleId}`}
            </Link>
          </Detail>
        )}
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
              <span className="font-mono text-meta text-muted tabular" title={dateTime(e.at)}>
                {stamp(e.at)}
              </span>
              <span className="flex min-w-0 flex-wrap items-center gap-1.5">
                {e.fromState === null || e.fromState === e.toState ? null : (
                  <span className="inline-flex items-center gap-1.5 font-mono text-meta text-muted">
                    {taskStateLabel(e.fromState)}
                    <IconArrowRight size={12} label="to" />
                  </span>
                )}
                <TaskStateBadge state={e.toState} />
                {e.detail === "" ? null : (
                  <span className="min-w-0 font-mono text-meta break-words text-muted">{e.detail}</span>
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
  const api = useApi();
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
          <p className="text-small whitespace-pre-wrap">{task.notes}</p>
        )
      ) : (
        <div className="flex flex-col gap-2">
          <SnippetTextarea rows={4} value={draft} onValueChange={setDraft} projectId={task.projectId} autoFocus aria-label="Notes" />
          <ErrorText error={save.error} />
        </div>
      )}
    </Panel>
  );
}

function FollowupPanel({
  taskId,
  projectId,
  inputRef,
}: {
  taskId: number;
  projectId: number;
  inputRef: Ref<HTMLTextAreaElement>;
}) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const [message, setMessage] = useState("");
  const [mode, setMode] = useState<SendMode>("auto");
  const images = useImageSet();
  const withImages = images.items.length > 0;
  const send = useMutation({
    // Images go as one new message (t3_thread_send_attachments); T3 takes no mode for them.
    mutationFn: () =>
      api.followupTask(taskId, withImages ? { message, attachmentIds: images.ids } : { message, mode }),
    onSuccess: async () => {
      setMessage("");
      images.clear();
      await invalidate();
    },
  });
  const ready = withImages ? !images.uploading && !images.failed : message.trim() !== "";
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (ready) send.mutate();
  };

  return (
    <Panel title="Follow-up">
      <form onSubmit={submit} className="flex flex-col gap-2.5">
        <ImageDropzone set={images} label="Follow-up images" disabled={send.isPending}>
          <SnippetTextarea
            ref={inputRef}
            rows={3}
            value={message}
            onValueChange={setMessage}
            projectId={projectId}
            placeholder="Message to send to the thread"
            aria-label="Follow-up message"
          />
        </ImageDropzone>
        <div className="flex flex-wrap items-center gap-2.5">
          <fieldset
            disabled={withImages}
            title={withImages ? "Images send as a new message; mode doesn't apply" : undefined}
            className="min-w-0 disabled:opacity-45"
          >
            <Segmented options={SEND_OPTIONS} value={mode} onChange={setMode} label="Send mode" small />
          </fieldset>
          {withImages ? <span className="font-mono text-meta-sm text-muted">sends as a new message</span> : null}
          <Button type="submit" variant="primary" small className="ml-auto" busy={send.isPending} disabled={!ready}>
            Send
          </Button>
        </div>
        <ErrorText error={send.error} />
      </form>
    </Panel>
  );
}
