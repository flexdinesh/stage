import { LaunchRecovery } from "../components/LaunchRecovery.tsx";
import { useMutation } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { useApi, usePlatform } from "../platform/context.tsx";
import { useHarnesses, useInvalidateWork, useTask } from "../api/queries.ts";
import { useSessionDestination } from "../lib/hostState.ts";
import { type TaskDetail, type TaskEvent, type Thread } from "../api/types.ts";
import { CopyLinkPair } from "../components/StageLinks.tsx";
import { HandoffRecord } from "../components/Handoff.tsx";
import { IconArrowRight } from "../components/icons.tsx";
import { JournalPanel } from "../components/Journal.tsx";
import { AcceptedResult } from "../components/Completion.tsx";
import { StateControl } from "../components/StateControl.tsx";
import { LastWordPanel } from "../components/LastWord.tsx";
import { OpeningPrompt, PromptPanel } from "../components/PromptPanel.tsx";
import { TaskStateBadge, ThreadStatusBadge, taskStateLabel } from "../components/StateBadge.tsx";
import { ImageStrip } from "../components/Images.tsx";
import { BatchPanel } from "../components/CreatedTasks.tsx";
import { NoTreePanel, TreeBar, TreePanel } from "../components/trees/TreePanel.tsx";
import { TaskFormModal } from "../components/TaskForm.tsx";
import {
  Alert,
  AppButton,
  Button,
  Chip,
  CopyButton,
  Detail,
  Details,
  EmptyState,
  ErrorText,
  ExternalLink,
  Meta,
  Muted,
  PageHeader,
  Panel,
  QueryState,
  RelTime,
  Row,
  Skeleton,
  SkeletonBar,
  TextLink,
  cx,
  ConfirmInline,
} from "../components/ui.tsx";
import { configModelText, dateTime, executionModes, workspaceText } from "../lib/format.ts";
import { pendingHandoffNote } from "../lib/handoff.ts";
import { pendingBaseNote, predecessorWorkspaceText } from "../lib/launchbase.ts";
import { useIdParam } from "../lib/params.ts";
import { SETTINGS_PATH } from "../lib/settings.ts";
import { browserTimeZone, wallTime } from "../lib/schedules.ts";
import { useMarkNotificationGroupsRead } from "../components/Notifications.tsx";
import { canStart, canStop, isActive } from "../lib/tasks.ts";
import { treeTaskPath } from "../lib/trees.ts";
import { isRetired, resumeCommand, retiredHint, retiredNotice } from "../lib/harnesses.ts";
import { ReplacedChip } from "../components/HarnessName.tsx";
import { HarnessConnectionState } from "../components/ConnectionState.tsx";
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
    <QueryState isPending={task.isPending} error={task.error} hasData={task.data !== undefined} skeleton={<TaskDetailSkeleton />}>
      {task.data === undefined ? null : <TaskBody key={id} task={task.data} />}
    </QueryState>
  );
}

/** Mono text lines as bars (prompt, replies). */
function LineBars({ widths }: { widths: readonly string[] }) {
  return (
    <div className="flex flex-col gap-2.5 py-1">
      {widths.map((w, i) => (
        <SkeletonBar key={i} className={cx("h-3", w)} />
      ))}
    </div>
  );
}

function StatePanelSkeleton({ className }: { className: string }) {
  return (
    <Panel title="State" className={className}>
      <div className="flex flex-col gap-2.5">
        <SkeletonBar className="h-control w-full" />
        <SkeletonBar className="h-3 w-40" />
      </div>
    </Panel>
  );
}

/**
 * TaskBody's frame while the task loads: header, chips, action row, then its two columns (State first on phones).
 * Titles known before the data render as text; everything else is bars.
 */
function TaskDetailSkeleton() {
  return (
    <Skeleton label="Loading task">
      <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
        <div className="flex min-w-0 flex-col gap-2.5 py-0.5">
          <SkeletonBar className="h-7 w-72 max-w-full" />
          <SkeletonBar className="h-3 w-36 lg:hidden" />
        </div>
        <SkeletonBar className="h-control-sm w-32" />
      </div>
      <div className="flex flex-col gap-3.5">
        <div className="flex flex-wrap items-center gap-2">
          <SkeletonBar className="h-control w-24" />
          <SkeletonBar className="h-control w-24" />
          <SkeletonBar className="h-control w-14" />
          <SkeletonBar className="ml-auto h-control w-20" />
        </div>
        <StatePanelSkeleton className="lg:hidden" />
        <div className="grid grid-cols-1 gap-3.5 lg:grid-cols-[minmax(0,1fr)_340px]">
          <div className="flex min-w-0 flex-col gap-3.5">
            <Panel title="Prompt">
              <LineBars widths={["w-full", "w-11/12", "w-full", "w-4/5", "w-2/3"]} />
            </Panel>
            <Panel title={<SkeletonBar onPanel className="h-2.5 w-28" />}>
              <LineBars widths={["w-5/6", "w-full", "w-1/2"]} />
            </Panel>
          </div>
          <div className="flex min-w-0 flex-col gap-3.5">
            <StatePanelSkeleton className="max-lg:hidden" />
            <Panel title="Tree">
              <LineBars widths={["w-full", "w-3/5"]} />
            </Panel>
            <Panel title={<SkeletonBar onPanel className="h-2.5 w-24" />}>
              <div className="flex flex-col gap-3 py-0.5">
                {["w-20", "w-16", "w-32", "w-12"].map((w) => (
                  <div key={w} className="flex items-center gap-4">
                    <SkeletonBar className="h-3 w-14" />
                    <SkeletonBar className={cx("h-3", w)} />
                  </div>
                ))}
              </div>
            </Panel>
          </div>
        </div>
      </div>
    </Skeleton>
  );
}

/** "10:42:03" today, "Nov 3 10:42" otherwise. */
function stamp(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const time = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
  if (d.toDateString() === new Date().toDateString()) return time;
  const day = d.toLocaleDateString([], { month: "short", day: "numeric" });
  return `${day} ${time.slice(0, 5)}`;
}

function TaskBody({ task }: { task: TaskDetail }) {
  const api = useApi();
  const { kindName } = useHarnesses();
  const navigate = useNavigate();
  const invalidate = useInvalidateWork();
  const [editing, setEditing] = useState(false);
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const start = useMutation({ mutationFn: () => api.startTask(task.id), onSettled: invalidate });
  const stop = useMutation({ mutationFn: () => api.stopTask(task.id), onSettled: invalidate });
  const duplicate = useMutation({
    mutationFn: () => api.duplicateTask(task.id),
    // The copy's page fetches itself; the rest refreshes behind the navigation.
    onSuccess: async (t) => {
      void invalidate();
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
  // A replaced connection keeps Start / Stop visible but disabled, with the reason.
  const retired = isRetired(task);
  const live = { ...task, connectionActive: true };
  const startable = canStart(live);
  const stoppable = canStop(live);
  const lockedHint = retired ? retiredHint(kindName(task.harness)) : undefined;
  const appUrl = useSessionDestination()(task);
  // A harness without a destination to open may offer a command that resumes the session in its own client.
  const resume = appUrl === null ? resumeCommand(task) : null;
  const phoneBar = appUrl !== null || resume !== null || stoppable;

  const openInApp = (variant: "primary" | "secondary", className?: string) =>
    appUrl === null ? (
      resume === null ? null : (
        <span className={className}>
          <CopyButton text={resume} label="Copy resume command" />
        </span>
      )
    ) : (
      <AppButton href={appUrl} variant={variant} {...(className === undefined ? {} : { className })}>
        Open in {kindName(task.harness)}
      </AppButton>
    );

  const stopButton = (className?: string) => (
    <Button busy={stop.isPending} disabled={retired} title={lockedHint} onClick={() => stop.mutate()} className={className}>
      Stop
    </Button>
  );

  return (
    <div className={cx(phoneBar && "pb-16 md:pb-0")}>
      <PageHeader
        title={task.title}
        actions={<CopyLinkPair subject={{ kind: "task", id: task.id, title: task.title }} />}
        trail={[
          { label: task.projectTitle, to: `/projects/${task.projectId}` },
          ...(task.tree === null ? [] : [{ label: task.tree.title, to: treeTaskPath(task.tree.id, task.id) }]),
          { label: `task ${task.id}` },
        ]}
        {...(task.startAt === null && task.scheduleId === null
          ? {}
          : {
              meta: (
                <>
                  {task.startAt === null ? null : (
                    <span title={dateTime(task.startAt)}>{`starts ${wallTime(task.startAt, task.startTz ?? browserTimeZone(), { zone: true })}`}</span>
                  )}
                  {task.startAt !== null && task.scheduleId !== null ? " · " : null}
                  {task.scheduleId === null ? null : (
                    <>
                      {"from "}
                      <TextLink to={`/schedules/${task.scheduleId}`} className="text-fg">
                        {task.scheduleTitle ?? `schedule #${task.scheduleId}`}
                      </TextLink>
                    </>
                  )}
                </>
              ),
            })}
      />
      {/* Flags only: state lives in the State panel, model and workspace in Config. */}
      {archived || task.adopted || retired ? (
        <div className="-mt-1 mb-3 flex flex-wrap gap-1.5">
          {archived ? <Chip title={`Archived ${dateTime(task.archivedAt ?? "")}`}>archived</Chip> : null}
          {task.adopted ? <Chip title="An existing harness session tracked by Stage">tracked session</Chip> : null}
          <ReplacedChip task={task} />
        </div>
      ) : null}

      <div className="flex flex-col gap-3.5">
        {task.tree === null ? null : <TreeBar task={task} tree={task.tree} />}
        {waiting ? (
          <Alert tone="need" actions={appUrl === null && resume === null ? undefined : openInApp("primary", "max-md:hidden")}>
            {kindName(task.harness)} needs input
          </Alert>
        ) : null}
        {retired ? <Alert tone="plain">{retiredNotice(task, kindName(task.harness))}</Alert> : null}

        <div className="flex flex-wrap items-center gap-2">
          {startable ? (
            <Button
              variant="primary"
              busy={start.isPending}
              disabled={retired}
              onClick={() => start.mutate()}
              {...(lockedHint !== undefined ? { title: lockedHint } : task.startAt === null ? {} : { title: "Starts now instead of at the scheduled time" })}
            >
              {task.state !== "backlog" ? "Restart" : task.startAt !== null ? "Start now" : "Start"}
            </Button>
          ) : null}
          {waiting ? null : openInApp("secondary", "max-md:hidden")}
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
              onClick={() => setConfirmingDelete(true)}
            >
              Delete
            </Button>
          ) : null}
        </div>
        {confirmingDelete && task.deletable ? (
          <ConfirmInline danger confirmLabel="Delete" busy={remove.isPending} onConfirm={() => remove.mutate()} onCancel={() => setConfirmingDelete(false)}>
            Delete task &ldquo;{task.title}&rdquo;? It never started; this can&apos;t be undone.
          </ConfirmInline>
        ) : null}

        <StatePanel task={task} className="lg:hidden" />
        <ErrorText error={actionError} />
        <LaunchRecovery task={task} />
        {task.lastError === null || task.state === "launching" ? null : (
          <Alert tone="bad">
            <span className="font-medium">Last error:</span> {task.lastError}
          </Alert>
        )}


        <div className="grid grid-cols-1 gap-3.5 lg:grid-cols-[minmax(0,1fr)_340px]">
          <div className="flex min-w-0 flex-col gap-3.5">
            {task.adopted && task.externalSessionId !== null ? (
              <OpeningPrompt taskId={task.id} live={isActive(task)} />
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
            {task.acceptances.map((acceptance) => <AcceptedResult key={acceptance.id} acceptance={acceptance} current={acceptance.id === task.acceptanceId} harness={task.harness} />)}
            <LastWordPanel task={task} />
            {task.artifactLinks.length === 0 ? null : <Panel title="Artifacts"><ul className="space-y-2">{task.artifactLinks.map((artifact) => <li key={artifact.url}><ExternalLink href={artifact.url} className="text-small">{artifact.label}</ExternalLink></li>)}</ul></Panel>}
            <JournalPanel owner={{ taskId: task.id }} projectId={task.projectId} />
            <TimelinePanel events={task.events} />
          </div>
          <div className="flex min-w-0 flex-col gap-3.5">
            <StatePanel task={task} className="max-lg:hidden" />
            {task.tree === null ? <NoTreePanel task={task} /> : <TreePanel task={task} tree={task.tree} />}
            <BatchPanel task={task} />
            <ThreadPanel task={task} thread={task.thread} />
            <ConfigPanel task={task} />
          </div>
        </div>
      </div>

      {phoneBar ? (
        <div className="fixed inset-x-0 bottom-[calc(3.5rem+env(safe-area-inset-bottom))] z-30 flex gap-2 border-t border-line bg-bg px-3 py-2.5 md:hidden">
          {openInApp(waiting ? "primary" : "secondary", "flex-1")}
          {stoppable ? stopButton("flex-1") : null}
        </div>
      ) : null}

      {editing ? <TaskFormModal task={task} onClose={() => setEditing(false)} /> : null}
    </div>
  );
}

/** The task's State picker: first in the side column on lg+, under the action row on phones (one shows per breakpoint). */
function StatePanel({ task, className }: { task: TaskDetail; className: string }) {
  return (
    <Panel title="State" className={className}>
      <StateControl task={task} tree={task.tree} />
    </Panel>
  );
}

function ThreadPanel({ task, thread }: { task: TaskDetail; thread: Thread | null }) {
  const destination = useSessionDestination();
  const title = `${useHarnesses().kindName(task.harness)} session`;
  const session = task.session;
  if (session === null) {
    return (
      <Panel title={title}>
        <Muted>{task.state === "launching" ? "Waiting for confirmation of the existing launch." : task.acceptanceId === null ? "No session yet. Start the task to launch one." : "Completed without an execution reference."}</Muted>
      </Panel>
    );
  }
  // Sessions Stage mirrors (thread history) carry run state; others only their id.
  return (
    <Panel title={title} meta={thread === null ? undefined : <>synced <RelTime at={thread.syncedAt} /></>}>
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
                  yes{thread.settledAt === null ? null : <span className="text-muted"> (<RelTime at={thread.settledAt} />)</span>}
                </span>
              ) : (
                "no (execution metadata only)"
              )}
            </Detail>
            <Detail label="Model">{thread.model ?? "-"}</Detail>
            <Detail label="Branch">{thread.branch ?? "-"}</Detail>
            <Detail label="Worktree">{thread.worktreePath ?? "-"}</Detail>
            <Detail label="Synced"><RelTime at={thread.syncedAt} /></Detail>
          </>
        )}
        <Detail label="Session id">
          <span className="flex min-w-0 items-center gap-2">
            <span className="min-w-0 truncate" title={session.externalSessionId}>
              {session.externalSessionId}
            </span>
            <CopyButton text={session.externalSessionId} label="Copy" title={session.externalSessionId} />
          </span>
        </Detail>
      </Details>
      {destination(task) === null && !isRetired(task) && resumeCommand(task) === null ? (
        <TextLink to={SETTINGS_PATH.connections} className="mt-3 inline-block text-small text-fg">
          Configure browser address
        </TextLink>
      ) : null}
    </Panel>
  );
}

function ConfigPanel({ task }: { task: TaskDetail }) {
  const { kindName } = useHarnesses();
  return (
    <Panel title="Config">
      <Details>
        <Detail label="Harness">
          <HarnessConnectionState connectionId={task.connectionId}>
            {kindName(task.harness)}
            {task.connectionLabel === "" ? null : <span className="text-muted"> {task.connectionLabel}</span>}
            {isRetired(task) ? <span className="text-muted"> (replaced)</span> : null}
          </HarnessConnectionState>
        </Detail>
        <Detail label="Model">{configModelText(task.executionConfig)}</Detail>
        <Detail label="Permission">{executionModes(task).permission || "Harness default"}</Detail>
        <Detail label="Mode">{executionModes(task).mode || "Harness default"}</Detail>
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
                <TextLink key={n} to={`/tasks/${n}`}>
                  #{n}
                </TextLink>
              ))}
            </span>
          </Detail>
        )}
        {task.originSession === null ? null : (
          <Detail label="Created by">
            <TextLink to={`/tasks/${task.id}?batch=1`}>{task.originSessionTitle ?? task.originSession.externalSessionId}</TextLink>
          </Detail>
        )}
        {task.scheduleId === null ? null : (
          <Detail label="Schedule">
            <TextLink to={`/schedules/${task.scheduleId}`}>{task.scheduleTitle ?? `#${task.scheduleId}`}</TextLink>
          </Detail>
        )}
        <Detail label="Attempts">{task.launchAttempt}</Detail>
        <Detail label="Created">
          <RelTime at={task.createdAt} />
        </Detail>
        <Detail label="Launched">
          <RelTime at={task.launchedAt} />
        </Detail>
        <Detail label="Finished">
          <RelTime at={task.finishedAt} />
        </Detail>
        <Detail label="Settled">
          <RelTime at={task.settledAt} />
        </Detail>
      </Details>
    </Panel>
  );
}

function TimelinePanel({ events }: { events: TaskEvent[] }) {
  return (
    <Panel title="Timeline" meta={`${events.length} event${events.length === 1 ? "" : "s"}`} flush>
      {events.length === 0 ? (
        <EmptyState inset>No events yet. State changes appear here once the task moves.</EmptyState>
      ) : (
        <ol>
          {events.map((e) => (
            <Row key={e.id} className="grid grid-cols-[auto_1fr] items-center gap-x-3 sm:grid-cols-[88px_1fr]">
              <Meta className="tabular" title={dateTime(e.at)}>
                {stamp(e.at)}
              </Meta>
              <span className="flex min-w-0 flex-wrap items-center gap-1.5">
                {e.fromState === null || e.fromState === e.toState ? null : (
                  <Meta className="inline-flex items-center gap-1.5">
                    {taskStateLabel(e.fromState)}
                    <IconArrowRight size={12} label="to" />
                  </Meta>
                )}
                <TaskStateBadge state={e.toState} />
                {e.detail === "" ? null : (
                  <Meta className="min-w-0 break-words">{e.detail}</Meta>
                )}
              </span>
            </Row>
          ))}
        </ol>
      )}
    </Panel>
  );
}
